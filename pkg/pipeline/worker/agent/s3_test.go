package agent_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/testutil"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
	"github.com/piper/piper/pkg/storage"
)

func mustReader(s string) *strings.Reader { return strings.NewReader(s) }

const testBucket = "piper-test"

// fakeS3 starts an in-process S3 server and returns a storage.S3Store.
func fakeS3(t *testing.T) (*testutil.Server, *storage.S3Store) {
	t.Helper()

	faker := gofakes3.New(s3mem.New())
	srv := testutil.NewIPv4Server(t, faker.Server())

	// Create the bucket — gofakes3 requires explicit bucket creation.
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	cli := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
	})
	if _, err := cli.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(testBucket)}); err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewS3Store(testBucket, "us-east-1", srv.URL, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	return srv, store
}

func fakeS3StorageURL(srv *testutil.Server) string {
	return fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test",
		testBucket, srv.URL)
}

// ─── S3 upload / download ─────────────────────────────────────────────────────

func TestRun_s3_artifact_upload(t *testing.T) {
	srv, store := fakeS3(t)
	masterSrv := fakeMasterServer(t)

	r, err := agent.New(agent.Config{
		MasterURL:  masterSrv.URL,
		OutputDir:  t.TempDir(),
		StorageURL: fakeS3StorageURL(srv),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "upload-step",
		Run: pipeline.Run{
			Command: []string{"sh", "-c", "echo 'artifact content' > $PIPER_OUTPUT_DIR/result.txt"},
		},
		Outputs: []pipeline.Artifact{
			{Name: "result", Path: "result.txt"},
		},
	})
	r.Run(context.Background(), task)

	// Verify that the file was uploaded to the store
	prefix := task.RunID + "/upload-step/result/"
	objs, err := store.List(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) == 0 {
		t.Errorf("no objects found under prefix %q", prefix)
	}
	for _, obj := range objs {
		t.Logf("uploaded: %s (%d bytes)", obj.Key, obj.Size)
	}
}

func TestRun_s3_artifact_input_output(t *testing.T) {
	srv, store := fakeS3(t)
	masterSrv := fakeMasterServer(t)

	// Upload an input file to the store as if a previous step had done so
	runID := "run-s3-test"
	err := store.Put(context.Background(),
		runID+"/prev-step/data/input.txt",
		mustReader("hello from previous step\n"),
		-1,
	)
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	r, err := agent.New(agent.Config{
		MasterURL:  masterSrv.URL,
		OutputDir:  outDir,
		InputDir:   outDir,
		StorageURL: fakeS3StorageURL(srv),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTaskWithRunID(t, pipeline.Step{
		Name: "consume-step",
		Run: pipeline.Run{
			Command: []string{"sh", "-c", "cat $PIPER_INPUT_DIR/data/input.txt > $PIPER_OUTPUT_DIR/out.txt"},
		},
		Inputs: []pipeline.Artifact{
			{Name: "data", From: "prev-step/data"},
		},
		Outputs: []pipeline.Artifact{
			{Name: "out", Path: "out.txt"},
		},
	}, runID)
	r.Run(context.Background(), task)

	// Verify that the output was uploaded to the store
	outPrefix := runID + "/consume-step/out/"
	objs, err := store.List(context.Background(), outPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) == 0 {
		t.Errorf("output artifact not found in store under %q", outPrefix)
	}
	for _, obj := range objs {
		t.Logf("output uploaded: %s", obj.Key)
	}
}

func TestRun_s3_cleansLocalWorkdirAfterRun(t *testing.T) {
	srv, _ := fakeS3(t)
	masterSrv := fakeMasterServer(t)

	outDir := t.TempDir()
	r, err := agent.New(agent.Config{
		MasterURL:  masterSrv.URL,
		OutputDir:  outDir,
		InputDir:   outDir,
		StorageURL: fakeS3StorageURL(srv),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "clean-step",
		Run: pipeline.Run{
			Command: []string{"sh", "-c", "echo data > $PIPER_OUTPUT_DIR/out.txt"},
		},
		Outputs: []pipeline.Artifact{{Name: "out", Path: "out.txt"}},
	})
	r.Run(context.Background(), task)

	stepDir := filepath.Join(outDir, task.RunID, "clean-step")
	if _, err := os.Stat(stepDir); !os.IsNotExist(err) {
		t.Errorf("local step dir should be removed after store upload, but still exists: %s", stepDir)
	}
}

func TestRun_s3_parallelConsumersUseIsolatedInputDirs(t *testing.T) {
	srv, store := fakeS3(t)
	masterSrv := fakeMasterServer(t)

	const runID = "run-parallel-inputs"
	if err := store.Put(context.Background(),
		runID+"/prepare/seed/seed.txt",
		mustReader("shared input\n"),
		-1,
	); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	r, err := agent.New(agent.Config{
		MasterURL:  masterSrv.URL,
		OutputDir:  outDir,
		InputDir:   outDir,
		StorageURL: fakeS3StorageURL(srv),
	})
	if err != nil {
		t.Fatal(err)
	}

	makeConsumer := func(name, command string) *proto.Task {
		return makeTaskWithRunID(t, pipeline.Step{
			Name: name,
			Run: pipeline.Run{
				Command: []string{"sh", "-c", command},
			},
			Inputs: []pipeline.Artifact{
				{Name: "seed", From: "prepare/seed"},
			},
			Outputs: []pipeline.Artifact{
				{Name: "result", Path: "result.txt"},
			},
		}, runID)
	}

	slow := makeConsumer("slow", "sleep 1; cat $PIPER_INPUT_DIR/seed/seed.txt > $PIPER_OUTPUT_DIR/result.txt")
	fast := makeConsumer("fast", "cat $PIPER_INPUT_DIR/seed/seed.txt > $PIPER_OUTPUT_DIR/result.txt")

	results := make(chan proto.TaskResult, 2)
	go func() { results <- r.Run(context.Background(), slow) }()
	go func() { results <- r.Run(context.Background(), fast) }()

	for range 2 {
		result := <-results
		if result.Status != proto.TaskStatusDone {
			t.Fatalf("task %s status = %q, error = %s", result.TaskID, result.Status, result.Error)
		}
	}
}

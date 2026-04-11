package runner_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/runner"
)

const testBucket = "piper-test"

// fakeS3 starts an in-process S3 server and returns an AWS SDK v2 client.
func fakeS3(t *testing.T) (*httptest.Server, *s3.Client) {
	t.Helper()

	faker := gofakes3.New(s3mem.New())
	srv := httptest.NewServer(faker.Server())
	t.Cleanup(srv.Close)

	client := newTestS3Client(t, srv.URL)

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(testBucket),
	})
	if err != nil {
		t.Fatal(err)
	}

	return srv, client
}

func newTestS3Client(t *testing.T, endpoint string) *s3.Client {
	t.Helper()
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

// ─── S3 upload / download ─────────────────────────────────────────────────────

func TestRun_s3_artifact_upload(t *testing.T) {
	srv, s3client := fakeS3(t)
	endpoint := srv.Listener.Addr().String()
	masterSrv := fakeMasterServer(t)

	r, err := runner.New(runner.Config{
		MasterURL:   masterSrv.URL,
		OutputDir:   t.TempDir(),
		S3Endpoint:  endpoint,
		S3AccessKey: "test",
		S3SecretKey: "test",
		S3Bucket:    testBucket,
		S3UseSSL:    false,
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

	// Verify that the file was uploaded to S3
	prefix := task.RunID + "/upload-step/result/"
	out, err := s3client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket: aws.String(testBucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Contents) == 0 {
		t.Errorf("no objects found under prefix %q", prefix)
	}
	for _, obj := range out.Contents {
		t.Logf("uploaded: %s (%d bytes)", aws.ToString(obj.Key), obj.Size)
	}
}

func TestRun_s3_artifact_input_output(t *testing.T) {
	srv, s3client := fakeS3(t)
	endpoint := srv.Listener.Addr().String()
	masterSrv := fakeMasterServer(t)

	// Upload an input file to S3 as if a previous step had done so
	runID := "run-s3-test"
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	_ = os.WriteFile(tmpFile, []byte("hello from previous step\n"), 0644)

	f, _ := os.Open(tmpFile)
	defer func() { _ = f.Close() }()
	_, err := s3client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(runID + "/prev-step/data/input.txt"),
		Body:   f,
	})
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	r, err := runner.New(runner.Config{
		MasterURL:   masterSrv.URL,
		OutputDir:   outDir,
		InputDir:    outDir,
		S3Endpoint:  endpoint,
		S3AccessKey: "test",
		S3SecretKey: "test",
		S3Bucket:    testBucket,
		S3UseSSL:    false,
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

	// Verify that the output was uploaded to S3
	outPrefix := runID + "/consume-step/out/"
	out, err := s3client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket: aws.String(testBucket),
		Prefix: aws.String(outPrefix),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Contents) == 0 {
		t.Errorf("output artifact not found in S3 under %q", outPrefix)
	}
	for _, obj := range out.Contents {
		t.Logf("output uploaded: %s", aws.ToString(obj.Key))
	}
}

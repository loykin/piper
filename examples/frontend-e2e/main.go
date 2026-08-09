package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	piper "github.com/loykin/piper"
	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/notebook"
	worker "github.com/loykin/piper/pkg/pipeline/worker"
	"github.com/loykin/piper/pkg/project"
)

const (
	bucket   = "piper-frontend-e2e"
	volumeID = "frontend-e2e-volume"
)

func main() {
	// "agent exec" subprocess invocations from the baremetal driver are
	// intercepted by agent_exec.go's init() (this binary imports the root
	// piper package), before main() ever runs.
	addr := flag.String("addr", "127.0.0.1:18080", "HTTP listen address")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "piper-frontend-e2e-*")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s3URL, s3Client, stopS3, err := startFakeS3(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer stopS3()

	repos, err := store.Open(filepath.Join(tmpDir, "piper.db"))
	if err != nil {
		log.Fatal(err)
	}

	workspace, err := filepath.Abs("examples/frontend-e2e/workspace")
	if err != nil {
		log.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repos.Project.Create(ctx, &project.Project{
		ID:          e2eProjectID,
		Name:        "E2E",
		Description: "Frontend E2E project",
	}); err != nil {
		log.Fatal(err)
	}
	if err := repos.NotebookVolume.Create(ctx, &notebook.NotebookVolume{
		ProjectID: e2eProjectID,
		ID:        volumeID,
		Label:     "Frontend E2E Workspace",
		WorkDir:   workspace,
		Status:    notebook.VolumeStatusReleased,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		log.Fatal(err)
	}

	p, err := piper.New(piper.Config{
		Repos:     repos,
		OutputDir: filepath.Join(tmpDir, "server-outputs"),
		Auth:      piper.AuthConfig{Trusted: true},
		Storage:   piper.StorageConfig{URL: s3URL},
		Server:    piper.ServerConfig{AllowInsecureDevKey: true},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	masterURL := "http://" + *addr
	extra := e2eHandler(s3Client)
	go func() {
		if err := p.Serve(ctx, piper.ServeOption{Addr: *addr, Extra: extra}); err != nil {
			log.Printf("server stopped: %v", err)
			cancel()
		}
	}()

	if err := waitHTTP(ctx, masterURL+"/health"); err != nil {
		log.Fatal(err)
	}

	// Create the default e2e project so that project-scoped API calls work.
	if err := createE2EProject(masterURL); err != nil {
		log.Fatalf("create e2e project: %v", err)
	}

	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{
			MasterURL:    masterURL,
			ID:           "frontend-e2e-worker",
			Concurrency:  4,
			Capabilities: []string{"pipeline", "notebook"},
		},
		Store: worker.StoreConfig{
			OutputDir:   filepath.Join(tmpDir, "worker-outputs"),
			RemoteStore: true,
		},
		Baremetal: worker.BaremetalConfig{
			MetaDir: filepath.Join(tmpDir, "worker-meta"),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		if err := w.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker stopped: %v", err)
			cancel()
		}
	}()

	fmt.Printf("FRONTEND_E2E_URL=%s\n", masterURL)
	<-ctx.Done()
}

func startFakeS3(ctx context.Context) (string, *s3.Client, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, nil, err
	}
	server := &http.Server{Handler: gofakes3.New(s3mem.New()).Server()}
	go func() { _ = server.Serve(listener) }()

	endpoint := "http://" + listener.Addr().String()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		_ = listener.Close()
		return "", nil, nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		_ = listener.Close()
		return "", nil, nil, err
	}

	storageURL := fmt.Sprintf(
		"s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test",
		bucket, endpoint,
	)
	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
	return storageURL, client, stop, nil
}

const e2eProjectID = "e2e"

func createE2EProject(masterURL string) error {
	body, _ := json.Marshal(map[string]any{"id": e2eProjectID, "name": "E2E"})
	resp, err := http.Post(masterURL+"/api/projects", "application/json",
		io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}
	return nil
}

func e2eHandler(client *s3.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/e2e/ready":
			w.WriteHeader(http.StatusNoContent)
		case "/e2e/objects":
			out, err := client.ListObjectsV2(r.Context(), &s3.ListObjectsV2Input{
				Bucket: aws.String(bucket),
				Prefix: aws.String(r.URL.Query().Get("prefix")),
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			keys := make([]string, 0, len(out.Contents))
			for _, object := range out.Contents {
				keys = append(keys, aws.ToString(object.Key))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(keys)
		case "/e2e/object":
			out, err := client.GetObject(r.Context(), &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(r.URL.Query().Get("key")),
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			defer func() { _ = out.Body.Close() }()
			_, _ = io.Copy(w, out.Body)
		}
	})
}

func waitHTTP(ctx context.Context, url string) error {
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

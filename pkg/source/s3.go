package source

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/piper/piper/pkg/pipeline"
)

type S3Fetcher struct {
	cfg Config
}

func (f *S3Fetcher) Fetch(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	if run.Path == "" {
		return "", fmt.Errorf("s3 source: path is required")
	}

	client, err := newS3Client(f.cfg)
	if err != nil {
		return "", fmt.Errorf("s3 client init failed: %w", err)
	}

	bucket := f.cfg.S3Bucket
	key := run.Path
	destFile := filepath.Join(destDir, filepath.Base(key))

	slog.Info("s3 download", "bucket", bucket, "key", key, "dest", destFile)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("s3 download failed: %w", err)
	}
	defer func() { _ = out.Body.Close() }()

	file, err := os.Create(destFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(file, out.Body); err != nil {
		return "", fmt.Errorf("s3 write failed: %w", err)
	}

	slog.Info("s3 fetch done", "file", destFile)
	return destFile, nil
}

func newS3Client(cfg Config) (*s3.Client, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKey, cfg.S3SecretKey, "",
		)),
	)
	if err != nil {
		return nil, err
	}

	opts := []func(*s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = true
		},
	}
	if cfg.S3Endpoint != "" {
		scheme := "http"
		if cfg.S3UseSSL {
			scheme = "https"
		}
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(scheme + "://" + cfg.S3Endpoint)
		})
	}

	return s3.NewFromConfig(awsCfg, opts...), nil
}

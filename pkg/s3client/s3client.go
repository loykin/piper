// Package s3client provides a shared S3 client constructor used by runner, source, and piper.
package s3client

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// New creates an S3-compatible client.
// Supports MinIO, SeaweedFS, Ceph RGW, Cloudflare R2, and AWS S3.
// endpoint is the host:port of the S3-compatible service; empty means AWS S3.
func New(endpoint, accessKey, secretKey string, useSSL bool) (*s3.Client, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	opts := []func(*s3.Options){func(o *s3.Options) { o.UsePathStyle = true }}
	if endpoint != "" {
		scheme := "http"
		if useSSL {
			scheme = "https"
		}
		ep := scheme + "://" + endpoint
		opts = append(opts, func(o *s3.Options) { o.BaseEndpoint = aws.String(ep) })
	}

	return s3.NewFromConfig(awsCfg, opts...), nil
}

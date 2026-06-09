package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Store implements Store for S3-compatible object storage (AWS S3, MinIO, SeaweedFS, R2).
type S3Store struct {
	client *s3.Client
	bucket string
}

// openS3 creates an S3Store from a parsed s3:// URL.
//
// URL format: s3://bucket?region=…&endpoint=http://…&s3ForcePathStyle=true&accessKey=…&secretKey=…
func openS3(u *url.URL) (*S3Store, error) {
	bucket := u.Host
	if bucket == "" {
		return nil, fmt.Errorf("s3 URL missing bucket: %s", u)
	}

	q := u.Query()
	region := q.Get("region")
	if region == "" {
		region = "us-east-1"
	}
	accessKey := q.Get("accessKey")
	secretKey := q.Get("secretKey")
	endpoint := q.Get("endpoint")
	forcePathStyle := q.Get("s3ForcePathStyle") == "true"

	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	if accessKey != "" || secretKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return nil, err
	}

	s3Opts := []func(*s3.Options){
		func(o *s3.Options) { o.UsePathStyle = forcePathStyle },
	}
	if endpoint != "" {
		ep := endpoint
		s3Opts = append(s3Opts, func(o *s3.Options) { o.BaseEndpoint = aws.String(ep) })
	}

	return &S3Store{
		client: s3.NewFromConfig(cfg, s3Opts...),
		bucket: bucket,
	}, nil
}

// NewS3Store creates an S3Store from explicit connection parameters.
// endpoint is the full URL (e.g. "http://minio:9000"); empty means AWS S3.
func NewS3Store(bucket, region, endpoint, accessKey, secretKey string) (*S3Store, error) {
	q := url.Values{}
	if region != "" {
		q.Set("region", region)
	}
	if endpoint != "" {
		q.Set("endpoint", endpoint)
		q.Set("s3ForcePathStyle", "true")
	}
	if accessKey != "" {
		q.Set("accessKey", accessKey)
	}
	if secretKey != "" {
		q.Set("secretKey", secretKey)
	}
	u := &url.URL{Scheme: "s3", Host: bucket, RawQuery: q.Encode()}
	return openS3(u)
}

func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, in)
	return err
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	var result []ObjectInfo
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if strings.HasSuffix(key, "/") {
				continue
			}
			var modAt time.Time
			if obj.LastModified != nil {
				modAt = *obj.LastModified
			}
			result = append(result, ObjectInfo{
				Key:        key,
				Size:       aws.ToInt64(obj.Size),
				ModifiedAt: modAt,
			})
		}
	}
	return result, nil
}

func (s *S3Store) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	objs := make([]types.ObjectIdentifier, len(keys))
	for i, k := range keys {
		k := k
		objs[i] = types.ObjectIdentifier{Key: &k}
	}
	_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(s.bucket),
		Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
	})
	return err
}

func (s *S3Store) URL(_ string) (string, bool) { return "", false }

func isS3NotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchKey"
}

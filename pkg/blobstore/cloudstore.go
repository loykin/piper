package blobstore

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob" // azblob:// driver
	_ "gocloud.dev/blob/gcsblob"   // gs:// driver
	"gocloud.dev/gcerrors"
)

// CloudStore implements Store using gocloud.dev/blob.
// Supports GCS (gs://) and Azure Blob Storage (azblob://).
type CloudStore struct {
	bucket *blob.Bucket
}

// openCloud creates a CloudStore from a raw URL (gs:// or azblob://).
func openCloud(ctx context.Context, rawURL string) (*CloudStore, error) {
	b, err := blob.OpenBucket(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return &CloudStore{bucket: b}, nil
}

func (s *CloudStore) Put(ctx context.Context, key string, r io.Reader, _ int64) error {
	opts := &blob.WriterOptions{}
	w, err := s.bucket.NewWriter(ctx, key, opts)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (s *CloudStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := s.bucket.NewReader(ctx, key, nil)
	if err != nil {
		if isCloudNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

func (s *CloudStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	iter := s.bucket.List(&blob.ListOptions{Prefix: prefix})
	var result []ObjectInfo
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(obj.Key, "/") {
			continue
		}
		result = append(result, ObjectInfo{
			Key:        obj.Key,
			Size:       obj.Size,
			ModifiedAt: obj.ModTime.UTC(),
		})
	}
	return result, nil
}

func (s *CloudStore) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if err := s.bucket.Delete(ctx, key); err != nil {
			if !isCloudNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (s *CloudStore) URL(key string) (string, bool) {
	ctx := context.Background()
	u, err := s.bucket.SignedURL(ctx, key, &blob.SignedURLOptions{
		Expiry: 15 * time.Minute,
	})
	if err != nil {
		return "", false
	}
	return u, true
}

// Close releases the underlying bucket connection.
func (s *CloudStore) Close() error { return s.bucket.Close() }

func isCloudNotFound(err error) bool {
	_ = errors.New // keep errors import
	return gcerrors.Code(err) == gcerrors.NotFound
}

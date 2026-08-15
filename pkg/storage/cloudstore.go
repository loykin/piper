package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"gocloud.dev/blob"
	azureblobdriver "gocloud.dev/blob/azureblob"
	"gocloud.dev/blob/gcsblob"
	"gocloud.dev/gcerrors"
	"gocloud.dev/gcp"
	"golang.org/x/oauth2/google"
)

// CloudStore implements Store using gocloud.dev/blob.
// Supports GCS (gs://) and Azure Blob Storage (azblob://).
type CloudStore struct {
	bucket *blob.Bucket
	scheme string // "gs" or "azblob", for backend reporting
}

// Scheme returns the URL scheme this store was opened with ("gs" or "azblob").
func (s *CloudStore) Scheme() string { return s.scheme }

// openCloud creates a CloudStore from a raw URL (gs:// or azblob://).
//
// Without explicit credentials, both backends fall back to gocloud.dev's
// ambient-environment auth (GOOGLE_APPLICATION_CREDENTIALS / metadata
// server for GCS, AZURE_STORAGE_ACCOUNT+KEY / azidentity default chain for
// Azure) — the Piper process's own environment, not anything configurable
// per-installation. When the URL carries explicit credential query
// parameters (injected by injectStorageCredential from a resolved gcs/azure
// credential), this bypasses ambient auth entirely and authenticates with
// exactly those values instead.
//
//	gs://bucket?serviceAccountKey=<base64 service-account JSON>
//	azblob://container?accountName=...&accountKey=<base64 shared key>
func openCloud(ctx context.Context, rawURL string) (*CloudStore, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("blobstore: invalid URL %q: %w", rawURL, err)
	}
	q := u.Query()

	switch u.Scheme {
	case "gs":
		if key := q.Get("serviceAccountKey"); key != "" {
			return openGCSWithCredentials(ctx, u.Host, key)
		}
	case "azblob":
		accountName := q.Get("accountName")
		accountKey := q.Get("accountKey")
		if accountName != "" && accountKey != "" {
			return openAzureWithCredentials(ctx, accountName, accountKey, u.Host)
		}
	}

	b, err := blob.OpenBucket(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return &CloudStore{bucket: b, scheme: u.Scheme}, nil
}

// openGCSWithCredentials authenticates with an explicit service-account key
// instead of Application Default Credentials.
func openGCSWithCredentials(ctx context.Context, bucket, base64ServiceAccountJSON string) (*CloudStore, error) {
	raw, err := base64.StdEncoding.DecodeString(base64ServiceAccountJSON)
	if err != nil {
		return nil, fmt.Errorf("gcs: decode serviceAccountKey: %w", err)
	}
	creds, err := google.CredentialsFromJSON(ctx, raw, "https://www.googleapis.com/auth/devstorage.read_write")
	if err != nil {
		return nil, fmt.Errorf("gcs: parse service account credentials: %w", err)
	}
	client, err := gcp.NewHTTPClient(gcp.DefaultTransport(), creds.TokenSource)
	if err != nil {
		return nil, fmt.Errorf("gcs: build authenticated client: %w", err)
	}
	b, err := gcsblob.OpenBucket(ctx, client, bucket, nil)
	if err != nil {
		return nil, err
	}
	return &CloudStore{bucket: b, scheme: "gs"}, nil
}

// openAzureWithCredentials authenticates with an explicit storage account
// shared key instead of the azidentity default credential chain.
func openAzureWithCredentials(ctx context.Context, accountName, base64AccountKey, containerName string) (*CloudStore, error) {
	accountKey, err := base64.StdEncoding.DecodeString(base64AccountKey)
	if err != nil {
		return nil, fmt.Errorf("azure: decode accountKey: %w", err)
	}
	svcURL, err := azureblobdriver.NewServiceURL(&azureblobdriver.ServiceURLOptions{AccountName: accountName})
	if err != nil {
		return nil, fmt.Errorf("azure: build service URL: %w", err)
	}
	containerURL, err := url.JoinPath(string(svcURL), containerName)
	if err != nil {
		return nil, fmt.Errorf("azure: build container URL: %w", err)
	}
	cred, err := azblob.NewSharedKeyCredential(accountName, string(accountKey))
	if err != nil {
		return nil, fmt.Errorf("azure: build shared key credential: %w", err)
	}
	client, err := container.NewClientWithSharedKeyCredential(containerURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: build container client: %w", err)
	}
	b, err := azureblobdriver.OpenBucket(ctx, client, nil)
	if err != nil {
		return nil, err
	}
	return &CloudStore{bucket: b, scheme: "azblob"}, nil
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

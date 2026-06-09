package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// UploadDir uploads all files under localDir to the store with the given prefix.
// localDir/foo/bar.txt → prefix/foo/bar.txt
func UploadDir(ctx context.Context, s Store, localDir, prefix string) error {
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(localDir, path)
		key := prefix + "/" + filepath.ToSlash(rel)
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return s.Put(ctx, key, f, info.Size())
	})
}

// DownloadDir downloads all objects with the given prefix into localDir.
// prefix/foo/bar.txt → localDir/foo/bar.txt
// Returns an error if no objects are found under the prefix.
func DownloadDir(ctx context.Context, s Store, prefix, localDir string) error {
	objs, err := s.List(ctx, prefix)
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return fmt.Errorf("no objects found at prefix %q", prefix)
	}
	for _, obj := range objs {
		rel := strings.TrimPrefix(obj.Key, prefix)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			// Prefix exactly matches the key (single-file URI): save as basename.
			rel = filepath.Base(obj.Key)
		}
		dest := filepath.Join(localDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		rc, err := s.Get(ctx, obj.Key)
		if err != nil {
			return fmt.Errorf("get %s: %w", obj.Key, err)
		}
		f, err := os.Create(dest)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(f, rc)
		_ = rc.Close()
		_ = f.Close()
		if copyErr != nil {
			return fmt.Errorf("write %s: %w", dest, copyErr)
		}
	}
	return nil
}

// ServeHTTP streams the given key from the store directly to an http.ResponseWriter.
func ServeHTTP(ctx context.Context, s Store, key string, w http.ResponseWriter) error {
	rc, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	_, err = io.Copy(w, rc)
	return err
}

// UploadPath uploads localPath to the store under prefix.
// If localPath is a regular file, it is stored at prefix/filename.
// If localPath is a directory, all files within are stored under prefix/.
func UploadPath(ctx context.Context, s Store, localPath, prefix string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		f, err := os.Open(localPath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return s.Put(ctx, prefix+"/"+info.Name(), f, info.Size())
	}
	return UploadDir(ctx, s, localPath, prefix)
}

// Open creates a Store from a URL string.
//
// Supported schemes:
//
//	s3://bucket?region=…&endpoint=http://…&s3ForcePathStyle=true&accessKey=…&secretKey=…
//	file:///abs/path  or  file://./rel/path
//	http://…  or  https://…
func Open(rawURL string, token string) (Store, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("blobstore: invalid URL %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "s3":
		return openS3(u)
	case "gs", "azblob":
		return openCloud(context.Background(), rawURL)
	case "file":
		// file:///abs/path  →  u.Host=="", u.Path=="/abs/path"
		// file://./rel      →  u.Host==".", u.Path=="/rel" — treat host+path
		root := u.Path
		if u.Host != "" && u.Host != "localhost" {
			root = u.Host + u.Path
		}
		return NewLocal(root)
	case "http", "https":
		return NewHTTPStore(rawURL, token), nil
	default:
		return nil, fmt.Errorf("blobstore: unsupported scheme %q in %q", u.Scheme, rawURL)
	}
}

package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// BlobStore is the narrow object-storage boundary used for captures. Timeline
// metadata and persona notes remain ordinary workspace files because they rely
// on append, directory walking, and small rewrites.
type BlobStore interface {
	Put(name string, data []byte, mime string) error
	Get(name string) ([]byte, string, error)
	// List names every stored blob, so a workspace can be copied out whole.
	List() ([]string, error)
}

type fileBlobs struct{ root string }

func (b fileBlobs) Put(name string, data []byte, _ string) error {
	return os.WriteFile(filepath.Join(b.root, name), data, 0o644)
}

func (b fileBlobs) Get(name string) ([]byte, string, error) {
	data, err := os.ReadFile(filepath.Join(b.root, name))
	return data, mimeFor(name), err
}

func (b fileBlobs) List() ([]string, error) {
	entries, err := os.ReadDir(b.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// GCSBlobs owns one reusable Cloud Storage client. Scope returns a user-bound
// object namespace; the bucket itself remains private.
type GCSBlobs struct {
	client *storage.Client
	bucket string
}

func NewGCSBlobs(ctx context.Context, bucket string) (*GCSBlobs, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCSBlobs{client: client, bucket: strings.TrimSpace(bucket)}, nil
}

func (g *GCSBlobs) Scope(prefix string) BlobStore {
	return gcsBlobScope{client: g.client, bucket: g.bucket, prefix: strings.Trim(prefix, "/")}
}

func (g *GCSBlobs) Close() error { return g.client.Close() }

type gcsBlobScope struct {
	client *storage.Client
	bucket string
	prefix string
}

func (g gcsBlobScope) object(name string) *storage.ObjectHandle {
	return g.client.Bucket(g.bucket).Object(path.Join(g.prefix, name))
}

func (g gcsBlobScope) Put(name string, data []byte, mime string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	writer := g.object(name).NewWriter(ctx)
	writer.ContentType = mime
	writer.CacheControl = "private, max-age=31536000, immutable"
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func (g gcsBlobScope) Get(name string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reader, err := g.object(name).NewReader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	mime := reader.Attrs.ContentType
	if mime == "" {
		mime = mimeFor(name)
	}
	return data, mime, nil
}

func (g gcsBlobScope) List() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	it := g.client.Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: g.prefix + "/"})
	var names []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		names = append(names, strings.TrimPrefix(attrs.Name, g.prefix+"/"))
	}
}

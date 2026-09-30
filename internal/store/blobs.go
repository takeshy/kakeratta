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
	// Delete removes one blob; a missing one is already gone.
	Delete(name string) error
	// List names every stored blob, so a workspace can be copied out whole.
	List() ([]string, error)
	// Purge removes every stored blob: the workspace is being closed.
	Purge() error
}

type fileBlobs struct{ root string }

func (b fileBlobs) Put(name string, data []byte, _ string) error {
	return os.WriteFile(filepath.Join(b.root, name), data, 0o644)
}

func (b fileBlobs) Get(name string) ([]byte, string, error) {
	data, err := os.ReadFile(filepath.Join(b.root, name))
	return data, mimeFor(name), err
}

func (b fileBlobs) Delete(name string) error {
	if err := os.Remove(filepath.Join(b.root, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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

func (b fileBlobs) Purge() error {
	names, err := b.List()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := os.Remove(filepath.Join(b.root, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
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

func (g gcsBlobScope) Delete(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.object(name).Delete(ctx); err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		return err
	}
	return nil
}

func (g gcsBlobScope) Purge() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return deleteObjects(ctx, g.client.Bucket(g.bucket), g.prefix)
}

// deleteObjects removes everything under one prefix of a bucket, one object at
// a time: a closed workspace has a few hundred captures, not millions.
func deleteObjects(ctx context.Context, bucket *storage.BucketHandle, prefix string) error {
	it := bucket.Objects(ctx, &storage.Query{Prefix: strings.Trim(prefix, "/") + "/"})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := bucket.Object(attrs.Name).Delete(ctx); err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
			return err
		}
	}
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

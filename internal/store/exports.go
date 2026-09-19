package store

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"cloud.google.com/go/storage"
)

// ExportTarget keeps a finished export outside the app and hands back a link to
// it. The point is that the bytes leave once: the browser takes them from
// storage, instead of holding a container open for the length of a download
// that the proxy in front of it would cut off anyway.
type ExportTarget interface {
	// Save streams one export into storage under name and returns a URL that
	// works for a short while, for that object alone.
	Save(ctx context.Context, name string, write func(io.Writer) error) (string, error)
}

// exportLifetime is how long the link works. It is a download the user starts
// straight away, not something to keep.
const exportLifetime = time.Hour

// Exports returns where one workspace's exports are kept: a prefix of its own,
// apart from the captures, so a lifecycle rule can sweep every export without
// touching anything the app still reads.
func (g *GCSBlobs) Exports(prefix string) ExportTarget {
	return gcsExports{client: g.client, bucket: g.bucket, prefix: strings.Trim(prefix, "/")}
}

type gcsExports struct {
	client *storage.Client
	bucket string
	prefix string
}

func (g gcsExports) Save(ctx context.Context, name string, write func(io.Writer) error) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	object := path.Join(g.prefix, name)
	writer := g.client.Bucket(g.bucket).Object(object).NewWriter(ctx)
	writer.ContentType = "application/zip"
	writer.CacheControl = "private, no-store"
	// The upload goes up a chunk at a time, and that chunk is the only part of
	// an export the app ever holds.
	writer.ChunkSize = 8 << 20
	if err := write(writer); err != nil {
		// Cancelling abandons the half-written object; Close would keep it.
		cancel()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	// Signing needs the service account to be allowed to sign for itself
	// (roles/iam.serviceAccountTokenCreator); the bucket itself stays private.
	return g.client.Bucket(g.bucket).SignedURL(object, &storage.SignedURLOptions{
		Method:  http.MethodGet,
		Expires: time.Now().Add(exportLifetime),
		Scheme:  storage.SigningSchemeV4,
		QueryParameters: url.Values{
			"response-content-disposition": {`attachment; filename="` + name + `"`},
		},
	})
}

// Package storage abstracts where uploaded binaries live.
//
// The prototype default is Supabase Storage (STORAGE_DRIVER=supabase),
// as specified. A local-disk driver is provided so the backend can be run
// and demonstrated with no external dependency.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Object is a stored binary.
type Object struct {
	Bucket    string
	Path      string
	Name      string
	MimeType  string
	Size      int64
	CreatedAt time.Time
}

// Driver is the contract both implementations satisfy.
type Driver interface {
	// Put writes an object. The key may contain forward slashes, which
	// Supabase Storage maps to folders and the local driver maps to
	// subdirectories.
	Put(ctx context.Context, bucket, key, mimeType string, r io.Reader, size int64) (*Object, error)
	// Open returns a reader for streaming an object through the API.
	Open(ctx context.Context, bucket, key string) (io.ReadCloser, *Object, error)
	// SignedURL returns a time-limited direct download link. Drivers that
	// cannot sign return a backend-proxied URL instead.
	SignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	// Delete removes an object. Missing objects are not an error.
	Delete(ctx context.Context, bucket, key string) error
	// Name identifies the driver in /health.
	Name() string
}

var ErrNotFound = errors.New("storage object not found")

// SanitizeKey keeps a caller-supplied storage key safe: no leading
// slashes, no "..", no backslashes, and a length ceiling.
func SanitizeKey(k string) (string, error) {
	k = strings.TrimSpace(k)
	k = strings.ReplaceAll(k, "\\", "/")
	k = strings.TrimPrefix(k, "/")
	if k == "" {
		return "", errors.New("empty storage key")
	}
	for _, seg := range strings.Split(k, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", errors.New("storage key contains an invalid path segment")
		}
	}
	k = path.Clean(k)
	if len(k) > 512 {
		return "", errors.New("storage key is too long")
	}
	return k, nil
}

// BuildKey produces a collision-resistant, org-scoped storage key.
func BuildKey(orgID, kind, filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	if ext == "" {
		ext = ".bin"
	}
	// The timestamp keeps keys unique without needing a UUID round trip.
	ts := time.Now().UTC().Format("20060102T150405.000")
	return fmt.Sprintf("%s/%s/%s%s", orgID, strings.ToLower(kind), ts, ext)
}

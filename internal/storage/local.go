package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local writes objects to disk under LocalDiskRoot and serves them back
// through the API's own /v1/files route. Intended for prototyping and
// for the `STORAGE_DRIVER=local` escape hatch; not for production.
type Local struct {
	root    string
	baseURL string
}

func NewLocal(root, baseURL string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create local storage root: %w", err)
	}
	return &Local{root: abs, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

func (l *Local) Name() string { return "local" }

func (l *Local) resolve(key string) (string, error) {
	clean, err := SanitizeKey(key)
	if err != nil {
		return "", err
	}
	full := filepath.Join(l.root, filepath.FromSlash(clean))
	// Defend against traversal even after Clean.
	if !strings.HasPrefix(full, l.root+string(os.PathSeparator)) {
		return "", errors.New("storage key escapes the storage root")
	}
	return full, nil
}

func (l *Local) Put(ctx context.Context, bucket, key, mimeType string, r io.Reader, size int64) (*Object, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return nil, err
	}
	f, err := os.Create(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var written int64
	if size > 0 {
		n, err := io.Copy(f, io.LimitReader(r, size+1))
		written = n
		if err != nil {
			return nil, err
		}
		if n > size {
			os.Remove(full)
			return nil, ErrTooLarge
		}
	} else {
		n, err := io.Copy(f, r)
		written = n
		if err != nil {
			return nil, err
		}
	}
	clean, _ := SanitizeKey(key)
	return &Object{
		Bucket: bucket, Path: clean, Name: filepath.Base(clean),
		MimeType: mimeType, Size: written, CreatedAt: time.Now().UTC(),
	}, nil
}

func (l *Local) Open(ctx context.Context, bucket, key string) (io.ReadCloser, *Object, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	clean, _ := SanitizeKey(key)
	return f, &Object{
		Bucket: bucket, Path: clean, Name: filepath.Base(clean),
		Size: st.Size(), CreatedAt: st.ModTime().UTC(),
	}, nil
}

// SignedURL returns a backend-proxied URL. The public path is
// unauthenticated (files in a bucket are course material by design), so
// the TTL is not enforced; the URL is regenerated per response instead.
func (l *Local) SignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	clean, err := SanitizeKey(key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/v1/files/%s/%s", l.baseURL, url.PathEscape(bucket), encodePath(clean)), nil
}

func (l *Local) Delete(ctx context.Context, bucket, key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

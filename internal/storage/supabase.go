package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Supabase talks to Supabase Storage's REST API using the service role
// key. Files are uploaded with the standard multipart endpoint and read
// back through a time-limited signed URL, so the browser never needs
// Supabase credentials.
type Supabase struct {
	baseURL string
	key     string
	http    *http.Client
}

func NewSupabase(projectURL, serviceKey string) *Supabase {
	return &Supabase{
		baseURL: strings.TrimRight(projectURL, "/"),
		key:     serviceKey,
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
}

func (s *Supabase) Name() string { return "supabase" }

func (s *Supabase) objectURL(bucket, key string) string {
	return fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, url.PathEscape(bucket), encodePath(key))
}

func encodePath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func (s *Supabase) headers() map[string]string {
	h := map[string]string{
		"Authorization": "Bearer " + s.key,
		"apikey":        s.key,
	}
	return h
}

func (s *Supabase) Put(ctx context.Context, bucket, key, mimeType string, r io.Reader, size int64) (*Object, error) {
	clean, err := SanitizeKey(key)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	mw := newMultipart(&body)
	if err := mw.writeFile("file", path.Base(clean), mimeType, r, size); err != nil {
		return nil, err
	}
	if err := mw.close(); err != nil {
		return nil, err
	}
	contentType := mw.contentType()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/storage/v1/object/"+url.PathEscape(bucket)+"/"+encodePath(clean), &body)
	if err != nil {
		return nil, err
	}
	for k, v := range s.headers() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supabase storage upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, storageErr(resp)
	}
	return &Object{
		Bucket: bucket, Path: clean, Name: path.Base(clean),
		MimeType: mimeType, Size: size, CreatedAt: time.Now().UTC(),
	}, nil
}

// Open is not used for the happy path (signed URLs are handed to the
// client instead) but keeps the Driver contract complete.
func (s *Supabase) Open(ctx context.Context, bucket, key string) (io.ReadCloser, *Object, error) {
	clean, err := SanitizeKey(key)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(bucket, clean), nil)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range s.headers() {
		req.Header.Set(k, v)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, nil, storageErr(resp)
	}
	return resp.Body, &Object{
		Bucket: bucket, Path: clean, Name: path.Base(clean),
		MimeType: resp.Header.Get("Content-Type"), Size: resp.ContentLength,
	}, nil
}

func (s *Supabase) SignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	clean, err := SanitizeKey(key)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	endpoint := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", s.baseURL, url.PathEscape(bucket), encodePath(clean))
	form := url.Values{"expiresIn": {fmt.Sprintf("%d", int(ttl.Seconds()))}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range s.headers() {
		req.Header.Set(k, v)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("supabase storage sign: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", storageErr(resp)
	}
	var out struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("supabase storage sign decode: %w", err)
	}
	if out.SignedURL == "" {
		return "", errors.New("supabase returned an empty signed url")
	}
	if strings.HasPrefix(out.SignedURL, "http") {
		return out.SignedURL, nil
	}
	return s.baseURL + out.SignedURL, nil
}

func (s *Supabase) Delete(ctx context.Context, bucket, key string) error {
	clean, err := SanitizeKey(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(bucket, clean), nil)
	if err != nil {
		return err
	}
	for k, v := range s.headers() {
		req.Header.Set(k, v)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return storageErr(resp)
	}
	return nil
}

func storageErr(resp *http.Response) error {
	var p struct {
		Error      string `json:"error"`
		Message    string `json:"message"`
		StatusCode string `json:"statusCode"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&p)
	msg := p.Message
	if msg == "" {
		msg = p.Error
	}
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("supabase storage (%d): %s", resp.StatusCode, msg)
}

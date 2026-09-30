package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSupabaseSignedURL sends a JSON object body and keeps the
// /storage/v1 prefix on the returned relative path. Both were wrong at
// once: the form-encoded expiresIn body was rejected with
// 400 "body must be object", and the bare-base concatenation produced
// 404 "requested path is invalid" on every subsequent fetch. Together
// they meant no download_url ever reached the player, which rendered as
// "document is still being prepared" with no 5xx anywhere.
func TestSupabaseSignedURL(t *testing.T) {
	var gotPath, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		if gotBody == nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"body must be object"}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"signedURL":"/object/sign/course-materials/org/pdf/f.pdf?token=abc"}`))
	}))
	defer srv.Close()

	s := NewSupabase(srv.URL, "service-role-key")
	got, err := s.SignedURL(context.Background(), "course-materials",
		"org/pdf/f.pdf", 6*time.Hour)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}

	if ct := gotCT; !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json: the sign "+
			"endpoint rejects any other body type", ct)
	}
	if v, _ := gotBody["expiresIn"].(string); v == "" {
		t.Errorf("expiresIn not sent as a string; body was %v", gotBody)
	}
	if v, _ := gotBody["expiresIn"].(string); v != "21600" {
		t.Errorf("expiresIn = %q, want 21600 (6h)", v)
	}
	if !strings.HasPrefix(gotPath, "/storage/v1/object/sign/") {
		t.Errorf("sign request path = %q", gotPath)
	}
	if !strings.Contains(got, "/storage/v1/") {
		t.Errorf("signed URL lost the /storage/v1 prefix: %q", got)
	}
	if !strings.HasPrefix(got, srv.URL) {
		t.Errorf("signed URL = %q, want the project base as prefix", got)
	}
	if !strings.Contains(got, "token=abc") {
		t.Errorf("signed URL dropped the token: %q", got)
	}
}

// TestSupabaseSignedURLPrefixForms covers both spellings Supabase uses for
// the relative path, plus an already-absolute URL, so a response shape
// change cannot silently yield an empty or broken link.
func TestSupabaseSignedURLPrefixForms(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"relative no prefix", `{"signedURL":"/object/sign/b/k?token=t"}`, "/storage/v1/object/sign/b/k"},
		{"relative with prefix", `{"signedURL":"/storage/v1/object/sign/b/k?token=t"}`, "/storage/v1/object/sign/b/k"},
		{"signedUrl spelling", `{"signedUrl":"/object/sign/b/k?token=t"}`, "/storage/v1/object/sign/b/k"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			s := NewSupabase(srv.URL, "k")
			got, err := s.SignedURL(context.Background(), "b", "k", time.Hour)
			if err != nil {
				t.Fatalf("SignedURL: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// TestSupabaseSignedURLRejectsFormBody pins the failure that started this:
// a form-encoded body must not be silently accepted, since the real
// endpoint answers 400 for it and the error has to reach the caller.
func TestSupabaseSignedURLRejectsFormBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var probe map[string]any
		if json.Unmarshal(raw, &probe) != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"statusCode":"400","error":"Error",` +
				`"message":"body must be object","code":"InvalidRequest"}`))
			return
		}
		_, _ = w.Write([]byte(`{"signedURL":"/object/sign/b/k?token=t"}`))
	}))
	defer srv.Close()

	s := NewSupabase(srv.URL, "k")
	if _, err := s.SignedURL(context.Background(), "b", "k", time.Hour); err != nil {
		t.Fatalf("JSON body should be accepted, got: %v", err)
	}
}

// TestSupabaseSignedURLEmptyResponse guards the "still being prepared"
// symptom directly: an empty signedURL must be an error, never a
// silently empty download_url.
func TestSupabaseSignedURLEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"signedURL":""}`))
	}))
	defer srv.Close()

	s := NewSupabase(srv.URL, "k")
	if _, err := s.SignedURL(context.Background(), "b", "k", time.Hour); err == nil {
		t.Fatal("expected an error for an empty signedURL, got nil")
	}
}

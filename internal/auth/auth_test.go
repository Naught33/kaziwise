package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/config"
)

func testService(t *testing.T, h http.Handler) *Service {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(&config.Config{
		SupabaseURL:     srv.URL,
		SupabaseService: "service-role-key",
	})
}

// adminUserPayload is the shape GoTrue returns from POST /auth/v1/admin/users:
// the user object on its own, with the id at the top level and no session
// wrapper.
const adminUserPayload = `{
  "id":"7ba6a3a8-36aa-4bd4-b040-4726ca69ca57",
  "aud":"authenticated",
  "email":"admin@kaziwise.dev",
  "app_metadata":{"provider":"email","providers":["email"]},
  "user_metadata":{"full_name":"Ada Admin","app_role":"org_admin"},
  "identities":[]
}`

// TestAdminCreateUserReadsTheID is the regression guard for the demo sign-in
// failure: the admin endpoint's response has no "user" object, so decoding it
// as a Session leaves the id empty and every demo profile gets linked to an
// auth id that does not exist.
func TestAdminCreateUserReadsTheID(t *testing.T) {
	svc := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/auth/v1/admin/users"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(adminUserPayload))
	}))

	au, err := svc.AdminCreateUser(context.Background(), "admin@kaziwise.dev", "Secret123", nil)
	if err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}
	got, err := au.UUID()
	if err != nil {
		t.Fatalf("UUID: %v", err)
	}
	if want := "7ba6a3a8-36aa-4bd4-b040-4726ca69ca57"; got.String() != want {
		t.Errorf("id = %s, want %s", got, want)
	}
	if got, want := au.UserMetaData["app_role"], "org_admin"; got != want {
		t.Errorf("user_metadata app_role = %v, want %v", got, want)
	}
}

// TestAdminUserPayloadIsNotASession documents why the two shapes are separate
// types: the same body decoded as a Session carries no id at all.
func TestAdminUserPayloadIsNotASession(t *testing.T) {
	var sess Session
	if err := json.Unmarshal([]byte(adminUserPayload), &sess); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sess.User.ID != "" {
		t.Errorf("Session.User.ID = %q, want empty for a bare user object", sess.User.ID)
	}
}

// TestAdminFindUserByEmailPages guards the re-seed path: the demo addresses
// already exist, so creation fails and the existing identity is found by
// paging the list.
func TestAdminFindUserByEmailPages(t *testing.T) {
	page := 0
	svc := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			// A full page, so the scan continues.
			users := make([]map[string]any, 0, adminUserPage)
			for i := 0; i < adminUserPage; i++ {
				users = append(users, map[string]any{
					"id":    uuid.New().String(),
					"email": "someone@else.test",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
			return
		}
		_, _ = w.Write([]byte(`{"users":[{"id":"80984cb4-38c1-4b3d-931d-bac439d252c0","email":"Admin@KaziWise.dev"}]}`))
	}))

	au, err := svc.AdminFindUserByEmail(context.Background(), "admin@kaziwise.dev")
	if err != nil {
		t.Fatalf("AdminFindUserByEmail: %v", err)
	}
	if got, want := au.ID, "80984cb4-38c1-4b3d-931d-bac439d252c0"; got != want {
		t.Errorf("id = %s, want %s", got, want)
	}
	if page != 2 {
		t.Errorf("requests = %d, want 2", page)
	}
}

func TestAdminFindUserByEmailNotFound(t *testing.T) {
	svc := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"users":[]}`))
	}))
	if _, err := svc.AdminFindUserByEmail(context.Background(), "nobody@kaziwise.dev"); err != ErrUserNotFound {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

// TestAdminUpdateUserUsesPut guards the verb: GoTrue answers this endpoint
// with PUT, and a PATCH comes back 405, so a password or metadata change
// would be dropped without any error at the call site.
func TestAdminUpdateUserUsesPut(t *testing.T) {
	svc := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, http.MethodPut; got != want {
			t.Errorf("method = %q, want %q", got, want)
		}
		if got, want := r.URL.Path, "/auth/v1/admin/users/7ba6a3a8-36aa-4bd4-b040-4726ca69ca57"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["password"] != "Secret123" {
			t.Errorf("password = %v, want the new password", body["password"])
		}
		_, _ = w.Write([]byte(adminUserPayload))
	}))

	err := svc.AdminUpdateUser(context.Background(),
		"7ba6a3a8-36aa-4bd4-b040-4726ca69ca57", "Secret123",
		map[string]any{"org_id": uuid.New().String()})
	if err != nil {
		t.Fatalf("AdminUpdateUser: %v", err)
	}
}

func TestAuthUserUUIDRejectsUnusableIDs(t *testing.T) {
	var missing *AuthUser
	if _, err := missing.UUID(); err == nil {
		t.Error("UUID on a nil user: want an error, got none")
	}
	blank := &AuthUser{}
	if _, err := blank.UUID(); err == nil {
		t.Error("UUID on an empty id: want an error, got none")
	}
	bad := &AuthUser{ID: "not-a-uuid"}
	if _, err := bad.UUID(); err == nil {
		t.Error("UUID on a non-uuid id: want an error, got none")
	}
}

// TestAuthErrorIsDuplicateEmail covers the distinction the seed path depends
// on: a duplicate address is recoverable by looking the user up, a transport
// failure is not.
func TestAuthErrorIsDuplicateEmail(t *testing.T) {
	cases := []struct {
		name string
		err  *AuthError
		want bool
	}{
		{"unprocessable", &AuthError{Status: http.StatusUnprocessableEntity, Code: "email_exists",
			Message: "User already registered"}, true},
		{"no code but says already", &AuthError{Status: http.StatusUnprocessableEntity,
			Message: "A user with this email address has already been registered"}, true},
		{"bad request", &AuthError{Status: http.StatusBadRequest, Code: "validation_failed",
			Message: "Email is invalid"}, false},
		{"server error", &AuthError{Status: http.StatusInternalServerError,
			Message: "something broke"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.IsDuplicateEmail(); got != tc.want {
				t.Errorf("IsDuplicateEmail() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAdminCreateUserSurfacesTheErrorCode checks the code reaches the caller,
// since that is what identifies a duplicate address.
func TestAdminCreateUserSurfacesTheErrorCode(t *testing.T) {
	svc := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"email_exists","msg":"User already registered"}`))
	}))
	_, err := svc.AdminCreateUser(context.Background(), "admin@kaziwise.dev", "Secret123", nil)
	if err == nil {
		t.Fatal("want an error for a duplicate address")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("err = %T, want *AuthError", err)
	}
	if authErr.Code != "email_exists" || !authErr.IsDuplicateEmail() {
		t.Errorf("err = %+v, want a duplicate email code", authErr)
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("message = %q, want the provider's description", err.Error())
	}
}

// Package auth wraps Supabase Auth and provides the JWT layer the API
// authenticates with.
//
// Two modes are supported (AUTH_MODE in .env):
//
//	supabase - verify the JWT that Supabase Auth issues. The browser signs
//	           in with supabase-js and sends the access token as a
//	           Bearer header. This is the production shape.
//	local    - the backend also accepts a self-signed token it minted from
//	           POST /v1/auth/login. Passwords are still checked against
//	           Supabase Auth when credentials are present, and a Supabase
//	           session is returned alongside the local token. This exists
//	           so the prototype can be driven end to end without a
//	           running Supabase project.
package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/kaziwise/kaziwise_backend/internal/config"
)

// Role mirrors domain.Role without importing domain (avoids a cycle
// because domain is used by the middleware in this same package tree).
type Role = string

const (
	RoleSuperAdmin = "super_admin"
	RoleOrgAdmin   = "org_admin"
	RoleManager    = "manager"
	RoleLearner    = "learner"
)

// Claims is the normalised identity the API authorises on.
type Claims struct {
	UserID     uuid.UUID // KaziWise profile id
	AuthUserID uuid.UUID // Supabase auth.users id
	OrgID      uuid.UUID
	Email      string
	Role       Role
	Name       string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Issuer     string
	Local      bool // minted by this backend (AUTH_MODE=local)
}

func (c Claims) IsAdmin() bool { return c.Role == RoleSuperAdmin || c.Role == RoleOrgAdmin }
func (c Claims) IsStaff() bool { return c.IsAdmin() || c.Role == RoleManager }

// IsLearner reports whether the caller is a training-only account. Staff
// are deliberately not learners, so learner endpoints stay closed to them.
func (c Claims) IsLearner() bool { return c.Role == RoleLearner }

// roleRank orders the staff roles for the nested permission check. Learner
// deliberately has no rank: it is a separate axis, not the bottom of the
// staff ladder, and is gated by Claims.IsLearner instead.
func roleRank(role string) int {
	switch role {
	case RoleSuperAdmin:
		return 3
	case RoleOrgAdmin:
		return 2
	case RoleManager:
		return 1
	default:
		return 0
	}
}

// RoleAtLeast reports whether role is want or outranks it, so a super admin
// is accepted wherever an org admin is and an org admin wherever a manager
// is. The comparison only runs over the staff ladder: asking for a learner
// never matches, and a learner never matches a staff requirement.
func RoleAtLeast(role, want string) bool {
	got, need := roleRank(role), roleRank(want)
	return got > 0 && need > 0 && got >= need
}

// Service is the authentication gateway used by the HTTP middleware and
// the auth handlers.
type Service struct {
	cfg  *config.Config
	http *http.Client

	// Supabase legacy/new JWT secret (HS256).
	hmacKey []byte
	// JWKS for RS256/ES256 projects.
	jwksURL string
	jwks    *jwksCache

	// bcrypt-hashed passwords for AUTH_MODE=local fallback identities.
	localMu    sync.RWMutex
	localIdent map[string]localIdentity
}

type localIdentity struct {
	orgID        uuid.UUID
	profile      uuid.UUID
	role         Role
	name         string
	passwordHash []byte
	email        string
	supabaseUID  uuid.UUID
}

// New builds the auth service.
func New(cfg *config.Config) *Service {
	s := &Service{
		cfg:        cfg,
		http:       &http.Client{Timeout: 20 * time.Second},
		hmacKey:    []byte(cfg.SupabaseJWTSec),
		jwksURL:    cfg.SupabaseJWKSURL,
		localIdent: map[string]localIdentity{},
	}
	if cfg.SupabaseJWKSURL != "" {
		s.jwks = &jwksCache{url: cfg.SupabaseJWKSURL, client: s.http}
	}
	return s
}

// ---------------------------------------------------------------------
// JWT verification
// ---------------------------------------------------------------------

// Verify parses and validates a bearer token, returning normalised claims.
func (s *Service) Verify(ctx context.Context, tokenStr string) (*Claims, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return nil, errors.New("empty token")
	}

	alg, kid, err := tokenHeader(tokenStr)
	if err != nil {
		return nil, err
	}

	var key any
	var wantAlg []string
	switch alg {
	case "HS256", "HS384", "HS512":
		if len(s.hmacKey) == 0 {
			return nil, errors.New("token is HS256 but SUPABASE_JWT_SECRET is not configured")
		}
		key = s.hmacKey
		wantAlg = []string{"HS256", "HS384", "HS512"}
	case "RS256", "RS384", "RS512", "ES256", "ES384":
		if s.jwks == nil {
			return nil, errors.New("asymmetric tokens require SUPABASE_URL or SUPABASE_JWKS_URL")
		}
		k, err := s.jwks.keyFor(ctx, alg, kid)
		if err != nil {
			return nil, err
		}
		key = k
		wantAlg = []string{alg}
	default:
		return nil, fmt.Errorf("unsupported token algorithm %q", alg)
	}

	opts := []jwt.ParserOption{jwt.WithValidMethods(wantAlg)}
	if s.cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(s.cfg.Issuer))
	}
	if s.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(s.cfg.Audience))
	}

	parsed, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) { return key, nil }, opts...)
	if err != nil {
		return nil, err
	}
	mapClaims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, errors.New("token is not valid")
	}
	return claimsFromMap(tokenStr, mapClaims)
}

func claimsFromMap(token string, m jwt.MapClaims) (*Claims, error) {
	c := &Claims{Issuer: str(m, "iss")}

	if v, err := floatClaim(m, "exp"); err == nil && v > 0 {
		c.ExpiresAt = time.Unix(int64(v), 0).UTC()
	} else {
		c.ExpiresAt = time.Now().Add(time.Hour)
	}
	if v, err := floatClaim(m, "iat"); err == nil && v > 0 {
		c.IssuedAt = time.Unix(int64(v), 0).UTC()
	}

	c.Email = str(m, "email")
	if c.Email == "" {
		c.Email = str(m, "preferred_username")
	}
	if nm := str(m, "name"); nm != "" {
		c.Name = nm
	} else if meta, ok := m["user_metadata"].(map[string]any); ok {
		if v, ok := meta["full_name"].(string); ok {
			c.Name = v
		}
	}

	// sub is the Supabase auth user id.
	if sub := str(m, "sub"); sub != "" {
		if id, err := uuid.Parse(sub); err == nil {
			c.AuthUserID = id
		}
	}

	// org_id / role come from user_metadata in Supabase, or from our own
	// signed claims in local mode. The role never comes from the stock
	// Supabase `role` claim, which is always "authenticated".
	meta, _ := m["user_metadata"].(map[string]any)
	for _, key := range []string{"org_id", "orgId", "organisation_id"} {
		if v := str(m, key); v != "" {
			if id, err := uuid.Parse(v); err == nil {
				c.OrgID = id
			}
			break
		}
	}
	if c.OrgID == uuid.Nil && meta != nil {
		if v, ok := meta["org_id"].(string); ok {
			if id, err := uuid.Parse(v); err == nil {
				c.OrgID = id
			}
		}
	}
	c.Role = str(m, "app_role")
	if c.Role == "" && meta != nil {
		if v, ok := meta["app_role"].(string); ok {
			c.Role = v
		}
	}
	if meta != nil {
		if v, ok := meta["local_auth"].(bool); ok {
			c.Local = v
		}
	}
	if !isKnownRole(c.Role) {
		c.Role = ""
	}
	// Local tokens carry the KaziWise profile id in `profile_id`.
	if v := str(m, "profile_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			c.UserID = id
		}
	}
	return c, nil
}

// tokenHeader returns the signing algorithm and key id of a compact JWT.
func tokenHeader(token string) (alg, kid string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", errors.New("token is not a JWT")
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", errors.New("malformed token header")
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdr, &h); err != nil {
		return "", "", errors.New("malformed token header")
	}
	if h.Alg == "" {
		return "", "", errors.New("token header has no alg")
	}
	return h.Alg, h.Kid, nil
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func floatClaim(m map[string]any, k string) (float64, error) {
	switch v := m[k].(type) {
	case float64:
		return v, nil
	case json.Number:
		return v.Float64()
	}
	return 0, errors.New("absent")
}

func isKnownRole(r string) bool {
	switch r {
	case RoleSuperAdmin, RoleOrgAdmin, RoleManager, RoleLearner:
		return true
	}
	return false
}

// ---------------------------------------------------------------------
// Token minting (AUTH_MODE=local only)
// ---------------------------------------------------------------------

// Mint issues a locally signed access token. Refused unless
// ALLOW_LOCAL_AUTH is enabled.
func (s *Service) Mint(ident localIdentity, ttl time.Duration) (string, time.Time, error) {
	if !s.cfg.AllowLocalAuth {
		return "", time.Time{}, errors.New("local token minting is disabled (set ALLOW_LOCAL_AUTH=true)")
	}
	secret := s.hmacKey
	if len(secret) == 0 {
		secret = []byte("kaziwise-local-dev-secret")
	}
	now := time.Now()
	exp := now.Add(ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":        ident.supabaseUID.String(),
		"profile_id": ident.profile.String(),
		"org_id":     ident.orgID.String(),
		"app_role":   ident.role,
		"role":       "authenticated",
		"email":      ident.email,
		"name":       ident.name,
		"iss":        firstNonEmpty(s.cfg.Issuer, "kaziwise-local"),
		"iat":        now.Unix(),
		"exp":        exp.Unix(),
		"user_metadata": map[string]any{
			"app_role":   ident.role,
			"org_id":     ident.orgID.String(),
			"local_auth": true,
			"full_name":  ident.name,
		},
	})
	signed, err := tok.SignedString(secret)
	return signed, exp, err
}

// RegisterLocalIdentity records a password so AUTH_MODE=local can
// authenticate without Supabase.
func (s *Service) RegisterLocalIdentity(orgID, profile, supabaseUID uuid.UUID, role, name, email, passwordHash string) {
	if !s.cfg.AllowLocalAuth {
		return
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	s.localIdent[strings.ToLower(email)] = localIdentity{
		orgID: orgID, profile: profile, role: role, name: name, email: email,
		passwordHash: []byte(passwordHash), supabaseUID: supabaseUID,
	}
}

// LookupLocalIdentity finds a locally registered identity by email.
func (s *Service) LookupLocalIdentity(email string) (localIdentity, bool) {
	s.localMu.RLock()
	defer s.localMu.RUnlock()
	id, ok := s.localIdent[strings.ToLower(strings.TrimSpace(email))]
	return id, ok
}

// HasLocalIdentities reports whether the local store was populated
// (used by /v1/auth/dev-identities to help the prototype front end).
func (s *Service) HasLocalIdentities() bool {
	s.localMu.RLock()
	defer s.localMu.RUnlock()
	return len(s.localIdent) > 0
}

// VerifyLocalPassword checks a plaintext password against the stored
// bcrypt hash for a local identity. The boolean reports whether the email
// is known locally, so the caller can tell "no such user" from "wrong
// password" without leaking which accounts exist.
func (s *Service) VerifyLocalPassword(email, password string) (ok bool, known bool) {
	ident, found := s.LookupLocalIdentity(email)
	known = found
	if !known {
		return false, false
	}
	if len(ident.passwordHash) == 0 {
		// Registered without a usable hash: the local path cannot
		// authenticate this identity.
		return false, true
	}
	return bcrypt.CompareHashAndPassword(ident.passwordHash, []byte(password)) == nil, true
}

// LocalIdentitySeed re-registers an identity, replacing any previous
// entry. It is used after a CSV import or a profile update so the local
// token path keeps matching the database.
func (s *Service) LocalIdentitySeed(orgID, profile, supabaseUID uuid.UUID, role, name, email, passwordHash string) {
	s.RegisterLocalIdentity(orgID, profile, supabaseUID, role, name, email, passwordHash)
}

// ---------------------------------------------------------------------
// Supabase Auth REST
// ---------------------------------------------------------------------

// Session is a Supabase Auth session. It is returned by the sign-in and
// refresh calls so handlers can hand the access token to the client.
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         struct {
		ID          string         `json:"id"`
		Email       string         `json:"email"`
		Phone       string         `json:"phone"`
		ConfirmedAt string         `json:"confirmed_at"`
		RawUserMeta map[string]any `json:"user_metadata"`
	} `json:"user"`
	WeakPassword any `json:"weak_password,omitempty"`
}

func (s *Service) supabaseURL(path string) string {
	return strings.TrimRight(s.cfg.SupabaseURL, "/") + path
}

// AuthUser is a GoTrue user object. The admin user endpoints return this
// shape on its own — id at the top level, with no session wrapper — so a
// Session cannot be used to read the id they produce. Decoding one as a
// Session yields an empty id, which is how a profile ends up linked to an
// auth user that does not exist.
type AuthUser struct {
	ID           string         `json:"id"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	ConfirmedAt  string         `json:"confirmed_at"`
	AppMetaData  map[string]any `json:"app_metadata"`
	UserMetaData map[string]any `json:"user_metadata"`
	CreatedAt    string         `json:"created_at"`
}

// UUID is the auth user id as a uuid. GoTrue issues uuids, so anything else
// is reported as an error rather than passed on as a zero id: callers key
// profiles on this value and a fabricated one produces an account that signs
// in successfully yet authorises as nobody.
func (u *AuthUser) UUID() (uuid.UUID, error) {
	if u == nil || u.ID == "" {
		return uuid.Nil, errors.New("supabase returned a user without an id")
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase user id %q is not a uuid", u.ID)
	}
	return id, nil
}

// authHeaders are the headers used for privileged (service role) calls.
func (s *Service) authHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	if s.cfg.SupabaseService != "" {
		h.Set("apikey", s.cfg.SupabaseService)
		h.Set("Authorization", "Bearer "+s.cfg.SupabaseService)
	}
	return h
}

// SignUp creates a Supabase auth user. email_confirm is true so the
// prototype does not need a mail provider.
func (s *Service) SignUp(ctx context.Context, email, password string, meta map[string]any) (*Session, error) {
	if s.cfg.SupabaseURL == "" {
		return nil, errors.New("SUPABASE_URL is not configured")
	}
	body := map[string]any{
		"email":         email,
		"password":      password,
		"email_confirm": true,
		"user_metadata": meta,
	}
	return s.postAuth(ctx, "/auth/v1/signup", body)
}

// AdminCreateUser provisions a user with the service role key (used for
// CSV employee import and campaign invitations).
func (s *Service) AdminCreateUser(ctx context.Context, email, password string, meta map[string]any) (*AuthUser, error) {
	if s.cfg.SupabaseURL == "" || s.cfg.SupabaseService == "" {
		return nil, errors.New("SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY are required")
	}
	body := map[string]any{
		"email":         email,
		"password":      password,
		"email_confirm": true,
		"user_metadata": meta,
	}
	var out AuthUser
	if err := s.doAuthJSON(ctx, http.MethodPost, "/auth/v1/admin/users", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrUserNotFound reports that no auth user exists for an address.
var ErrUserNotFound = errors.New("supabase auth user not found")

// adminUserPage caps one page of the admin user list.
const adminUserPage = 200

// adminUserPages caps how far the list is walked. GoTrue has no lookup by
// email, so a miss beyond this window is reported as not found rather than
// paging a large project indefinitely on every boot.
const adminUserPages = 25

// AdminFindUserByEmail locates an existing auth user by address. It is the
// other half of AdminCreateUser: creating an address that is already
// registered fails, and a profile still has to be linked to the identity
// that owns the sign-in.
func (s *Service) AdminFindUserByEmail(ctx context.Context, email string) (*AuthUser, error) {
	if s.cfg.SupabaseURL == "" || s.cfg.SupabaseService == "" {
		return nil, errors.New("SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY are required")
	}
	want := strings.ToLower(strings.TrimSpace(email))
	for page := 1; page <= adminUserPages; page++ {
		var out struct {
			Users []AuthUser `json:"users"`
		}
		path := fmt.Sprintf("/auth/v1/admin/users?page=%d&per_page=%d", page, adminUserPage)
		if err := s.doAuthJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		for i := range out.Users {
			if strings.ToLower(strings.TrimSpace(out.Users[i].Email)) == want {
				return &out.Users[i], nil
			}
		}
		if len(out.Users) < adminUserPage {
			break
		}
	}
	return nil, ErrUserNotFound
}

// AdminUpdateUser patches an existing auth user. GoTrue exposes this as PUT,
// not PATCH: a PATCH is answered with 405 and the change is silently lost.
func (s *Service) AdminUpdateUser(ctx context.Context, uid, password string, meta map[string]any) error {
	if s.cfg.SupabaseURL == "" || s.cfg.SupabaseService == "" {
		return errors.New("SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY are required")
	}
	body := map[string]any{}
	if password != "" {
		body["password"] = password
	}
	if meta != nil {
		body["user_metadata"] = meta
	}
	if len(body) == 0 {
		return nil
	}
	return s.doAuthJSON(ctx, http.MethodPut,
		"/auth/v1/admin/users/"+uid, body, nil)
}

// PasswordSignIn exchanges email+password for a Supabase session.
func (s *Service) PasswordSignIn(ctx context.Context, email, password string) (*Session, error) {
	if s.cfg.SupabaseURL == "" {
		return nil, errors.New("SUPABASE_URL is not configured")
	}
	return s.postAuth(ctx, "/auth/v1/token?grant_type=password", map[string]any{
		"email": email, "password": password,
	})
}

// RefreshToken exchanges a refresh token for a new session.
func (s *Service) RefreshToken(ctx context.Context, refresh string) (*Session, error) {
	if s.cfg.SupabaseURL == "" {
		return nil, errors.New("SUPABASE_URL is not configured")
	}
	return s.postAuth(ctx, "/auth/v1/token?grant_type=refresh_token", map[string]any{
		"refresh_token": refresh,
	})
}

// ForgotPassword triggers Supabase's recovery email.
func (s *Service) ForgotPassword(ctx context.Context, email, redirectTo string) error {
	if s.cfg.SupabaseURL == "" {
		return errors.New("SUPABASE_URL is not configured")
	}
	_, err := s.postAuth(ctx, "/auth/v1/recover", map[string]any{
		"email": email, "gotrue_meta_security": map[string]any{},
	})
	return err
}

// UpdatePassword sets a new password for the given bearer token.
func (s *Service) UpdatePassword(ctx context.Context, accessToken, newPassword string) error {
	if s.cfg.SupabaseURL == "" {
		return errors.New("SUPABASE_URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.supabaseURL("/auth/v1/user"),
		jsonBody(map[string]any{"password": newPassword}))
	if err != nil {
		return err
	}
	req.Header = s.authHeaders()
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return supabaseError(resp)
	}
	return nil
}

func (s *Service) postAuth(ctx context.Context, path string, body any) (*Session, error) {
	var out Session
	if err := s.doAuthJSON(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// doAuthJSON performs a service-role call and decodes the response into out.
// body may be nil for GET requests, where a body would be invalid.
func (s *Service) doAuthJSON(ctx context.Context, method, path string, body, out any) error {
	if s.cfg.SupabaseService == "" {
		return errors.New("SUPABASE_SERVICE_ROLE_KEY is required")
	}
	var payload io.Reader
	if body != nil {
		payload = jsonBody(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.supabaseURL(path), payload)
	if err != nil {
		return err
	}
	req.Header = s.authHeaders()
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("supabase auth unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return supabaseError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("supabase auth response: %w", err)
	}
	return nil
}

type supabaseErrPayload struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Msg              string `json:"msg"`
	Code             string `json:"code"`
}

// AuthError is a non-2xx response from Supabase Auth. The status and the
// provider's own code are kept so a caller can tell a duplicate address from
// a transport failure: creating a user that already exists has to fall back
// to a lookup rather than be treated as fatal.
type AuthError struct {
	Status  int
	Code    string
	Message string
}

func (e *AuthError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("supabase auth (%d)", e.Status)
	}
	return fmt.Sprintf("supabase auth (%d): %s", e.Status, e.Message)
}

// IsDuplicateEmail reports whether Supabase refused because the address is
// already registered.
func (e *AuthError) IsDuplicateEmail() bool {
	if e.Status != http.StatusUnprocessableEntity && e.Status != http.StatusConflict {
		return false
	}
	switch e.Code {
	case "email_exists", "email_address_exists", "user_already_exists", "":
		return true
	}
	return strings.Contains(strings.ToLower(e.Message), "already")
}

func supabaseError(resp *http.Response) error {
	var p supabaseErrPayload
	_ = json.NewDecoder(resp.Body).Decode(&p)
	msg := p.ErrorDescription
	if msg == "" {
		msg = p.Msg
	}
	if msg == "" {
		msg = p.Error
	}
	if msg == "" {
		msg = resp.Status
	}
	return &AuthError{Status: resp.StatusCode, Code: p.Code, Message: msg}
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

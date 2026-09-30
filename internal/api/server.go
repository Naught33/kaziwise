// Package api wires the HTTP surface: middleware, route table and
// handlers. Handlers stay thin — they decode input, resolve the caller's
// identity and role, and delegate to the service layer.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/config"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/service"
	"github.com/kaziwise/kaziwise_backend/internal/storage"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// Server holds the dependencies every handler needs.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	db      *store.DB
	svc     *service.Service
	auth    *auth.Service
	storage storage.Driver
	router  http.Handler
}

// New builds the server. The route table is assembled once here so the
// self-describing index and the live routes can never disagree.
func New(cfg *config.Config, log *slog.Logger, db *store.DB, authSvc *auth.Service,
	store storage.Driver) *Server {
	// A 500 is reported to the client as a generic message, so the cause is
	// recorded here. Without it a failing query is only visible as a status
	// code, with nothing to act on.
	httpx.SetInternalErrorLogger(func(err error) {
		log.Error("unhandled error", "error", err)
	})
	s := &Server{
		cfg:     cfg,
		log:     log,
		db:      db,
		svc:     service.New(db, log),
		auth:    authSvc,
		storage: store,
	}
	s.router = s.buildRouter()
	return s
}

// ---------------------------------------------------------------------
// Request context
// ---------------------------------------------------------------------

type ctxKey int

const claimsKey ctxKey = iota

// ClaimsFrom returns the caller's identity, or nil for a public route.
func ClaimsFrom(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(claimsKey).(*auth.Claims)
	return c
}

// ---------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------

// authenticate verifies the bearer token and loads the KaziWise profile.
//
// The org is resolved from the database rather than trusted from the
// token: a Supabase app_metadata claim can be stale, and the tenant must
// never come from client-controllable data.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			httpx.Fail(w, httpx.Unauthorized("An access token is required."))
			return
		}
		claims, err := s.auth.Verify(r.Context(), token)
		if err != nil {
			s.log.Debug("token rejected", "error", err)
			httpx.Fail(w, httpx.Unauthorized("Your session is invalid or has expired. Please sign in again."))
			return
		}
		if claims.OrgID == uuid.Nil {
			httpx.Fail(w, httpx.Unauthorized("Your account is not linked to an organisation."))
			return
		}

		// Resolve the profile so the request authorises on stored role and
		// org data, never on the token alone.
		var user *domain.User
		switch {
		case claims.UserID != uuid.Nil:
			user, err = s.db.UserByAnyID(r.Context(), claims.OrgID, claims.UserID, claims.AuthUserID)
		case claims.AuthUserID != uuid.Nil:
			user, err = s.db.UserByAuthID(r.Context(), claims.AuthUserID)
		default:
			user, err = s.db.UserByEmail(r.Context(), claims.OrgID, claims.Email)
		}
		if err != nil {
			if isNotFound(err) {
				httpx.Fail(w, httpx.NewError(http.StatusForbidden, "no_profile",
					"Your account is not set up in this organisation. Ask an administrator for access."))
				return
			}
			httpx.Fail(w, err)
			return
		}
		// A token minted for another tenant must not be usable here.
		if user.OrgID != claims.OrgID {
			httpx.Fail(w, httpx.Forbidden("Your token does not belong to this organisation."))
			return
		}
		if user.Status == domain.UserInactive {
			httpx.Fail(w, httpx.NewError(http.StatusForbidden, "account_inactive",
				"Your account is not active. Contact an administrator."))
			return
		}

		claims.UserID = user.ID
		claims.OrgID = user.OrgID
		claims.Role = string(user.Role)
		claims.Email = user.Email
		claims.Name = user.FullName
		s.db.TouchUserSeen(r.Context(), user.ID)

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRole wraps a handler with a role check. Staff roles nest, so a
// super admin is accepted wherever an org admin is.
func requireRole(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !roleAllowed(w, r, allowed) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireRoleFunc is requireRole for a single handler, so a route can be
// guarded inline: r.Get("/audit", requireRoleFunc(super, staff)(s.listAudit)).
func requireRoleFunc(allowed ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !roleAllowed(w, r, allowed) {
				return
			}
			next(w, r)
		}
	}
}

// roleAllowed writes the failure response and reports whether the caller
// may proceed, so both wrappers share one implementation of the check.
// Roles nest: a super admin passes a manager-only route.
func roleAllowed(w http.ResponseWriter, r *http.Request, allowed []string) bool {
	c := ClaimsFrom(r.Context())
	if c == nil {
		httpx.Fail(w, httpx.Unauthorized("An access token is required."))
		return false
	}
	for _, role := range allowed {
		if auth.RoleAtLeast(c.Role, role) {
			return true
		}
	}
	httpx.Fail(w, httpx.NewError(http.StatusForbidden, "forbidden",
		"Your role does not allow this action."))
	return false
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// recoverPanics turns a panic into a 500 without taking the process down.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic serving request",
					"path", r.URL.Path, "method", r.Method, "panic", v)
				httpx.Fail(w, httpx.Internal(errors.New("panic")))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogger records one line per request.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		attrs := []any{
			"method", r.Method, "path", r.URL.Path,
			"status", ww.status, "duration_ms", time.Since(start).Milliseconds(),
		}
		if c := ClaimsFrom(r.Context()); c != nil {
			attrs = append(attrs, "user", c.UserID, "role", c.Role, "org", c.OrgID)
		}
		if ww.status >= 500 {
			s.log.Error("request", attrs...)
		} else {
			s.log.Info("request", attrs...)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// cors answers preflight requests and sets the response headers.
func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.CORSAllowedOrigins {
		allowed[strings.TrimRight(o, "/")] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" && allowed[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
			h.Set("Access-Control-Expose-Headers", "Content-Disposition")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------
// Route table
// ---------------------------------------------------------------------

// Role groups used by the route table. super and staff may author content,
// a manager may only read and chase, a learner only their own records.
const (
	super  = auth.RoleSuperAdmin
	staff  = auth.RoleOrgAdmin
	manage = auth.RoleManager
)

// Router returns the assembled HTTP handler.
func (s *Server) Router() http.Handler {
	return s.router
}

// buildRouter builds the full route table.
func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(s.recoverPanics, s.requestLogger, s.cors)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, httpx.NewError(http.StatusNotFound, "route_not_found",
			"No endpoint matches "+r.Method+" "+r.URL.Path))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, httpx.NewError(http.StatusMethodNotAllowed, "method_not_allowed",
			r.Method+" is not allowed on this endpoint."))
	})

	// Liveness and readiness need no auth and no database.
	r.Get("/health", s.health)
	r.Get("/ready", s.ready)

	// The public certificate viewer.
	r.Get("/v1/certificates/public/{code}", s.publicCertificateHTML)
	r.Get("/v1/certificates/public/{code}.json", s.publicCertificateJSON)

	r.Route("/v1", func(v1 chi.Router) {
		v1.Route("/auth", s.authRoutes())

		// Everything below requires a valid session.
		v1.Group(func(priv chi.Router) {
			priv.Use(s.authenticate)
			s.registerRoutes(priv)
		})
	})

	// The API description is registered after the group above, and that
	// order matters. Mounting /v1 makes chi write a catch-all handler for
	// the mount path itself, which replaces a GET already registered on
	// the same path, so the index would 404. Registering it last restores
	// the specific GET; the mount's catch-all stays as the fallback for
	// other methods on the bare /v1 path.
	r.Get("/v1", s.apiIndex)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, map[string]any{
		"status":  "ok",
		"service": s.cfg.AppName,
		"env":     s.cfg.Env,
		"time":    time.Now().UTC(),
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		httpx.Fail(w, httpx.NewError(http.StatusServiceUnavailable, "database_unavailable",
			"The database is not reachable.").WithCause(err))
		return
	}
	httpx.JSON(w, map[string]any{"status": "ready", "database": "ok"})
}

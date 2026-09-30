package api

import (
	"net/http"
	"strings"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// apiIndex describes the versioned surface. The endpoint list is read from
// the live router, so the index can never advertise a path that does not
// exist or miss one that was added.
func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	entries := s.routeIndex()
	endpoints := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		endpoints = append(endpoints, map[string]string{
			"method": e.Method,
			"path":   e.Path,
			"auth":   publicOrBearer(e.Path),
		})
	}
	body := map[string]any{
		"service":   "kaziwise-api",
		"version":   "v1",
		"endpoints": endpoints,
	}
	httpx.JSON(w, body)
}

// publicOrBearer labels a route by whether the shared authenticate
// middleware guards it.
func publicOrBearer(path string) string {
	switch {
	case path == "/health", path == "/ready", path == "/version", path == "/metrics":
		return "none"
	case path == "/v1":
		return "none"
	case strings.HasPrefix(path, "/v1/auth/"),
		strings.HasPrefix(path, "/v1/certificates/public/"),
		strings.HasPrefix(path, "/v1/files/"):
		return "none"
	default:
		return "bearer"
	}
}

// getOrg returns the current organisation.
func (s *Server) getOrg(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	org, err := s.db.OrgByID(r.Context(), orgID)
	if err != nil {
		httpx.Fail(w, httpx.NotFound("organisation"))
		return
	}
	httpx.JSON(w, org)
}

type updateOrgRequest struct {
	Name     *string `json:"name"`
	Industry *string `json:"industry"`
	Country  *string `json:"country"`
	Timezone *string `json:"timezone"`
}

func (s *Server) updateOrg(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req updateOrgRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len([]rune(name)) < 2 {
			fields["name"] = "Use at least 2 characters."
		}
		req.Name = &name
	}
	if req.Timezone != nil && strings.TrimSpace(*req.Timezone) == "" {
		fields["timezone"] = "Use an IANA timezone, for example Africa/Nairobi."
	}
	if len(fields) > 0 {
		httpx.Fail(w, ValidationError("Please correct the highlighted fields.", fields))
		return
	}
	// The slug is derived from the name at creation and left alone, so
	// existing verification links and bookmarks keep working.
	if err := s.db.UpdateOrg(r.Context(), orgID, store.UpdateOrgParams{
		Name: req.Name, Industry: req.Industry,
		Country: req.Country, Timezone: req.Timezone,
	}); err != nil {
		httpx.Fail(w, err)
		return
	}
	org, err := s.db.OrgByID(r.Context(), orgID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "org.update", "organisation", orgID, nil)
	httpx.JSON(w, org)
}

// listAudit exposes the trail for compliance review.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	entries, total, err := s.db.ListAudit(r.Context(), orgID, p.Page, p.PerPage,
		strings.TrimSpace(p.RawQuery["entity"]))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if entries == nil {
		entries = []domain.AuditEntry{}
	}
	httpx.JSONMeta(w, entries, p.Meta(total))
}

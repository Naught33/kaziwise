package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// Certificates (screen 14)
// ---------------------------------------------------------------------

func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	var learnerID *uuid.UUID
	claims := ClaimsFrom(r.Context())
	// A learner only ever sees their own certificates.
	if claims.IsLearner() {
		learnerID = &actorID
	} else if raw := p.RawQuery["learner_id"]; raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			learnerID = &id
		}
	}
	certificates, total, err := s.db.ListCertificates(r.Context(), orgID, learnerID, p.Page, p.PerPage)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	shared := make([]domain.Certificate, 0, len(certificates))
	for _, c := range certificates {
		shared = append(shared, *s.svc.CertificateShare(&c, s.cfg.BaseURL))
	}
	httpx.JSONMeta(w, shared, p.Meta(total))
}

func (s *Server) getCertificate(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	cert, err := s.db.CertificateByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "certificate"))
		return
	}
	if ClaimsFrom(r.Context()).IsLearner() && cert.LearnerID != actorID {
		httpx.Fail(w, httpx.Forbidden("You can only view your own certificate."))
		return
	}
	httpx.JSON(w, s.svc.CertificateShare(cert, s.cfg.BaseURL))
}

type issueCertificateRequest struct {
	LearnerID   string  `json:"learner_id"`
	CourseID    string  `json:"course_id"`
	Score       float64 `json:"score"`
	CompletedAt *string `json:"completed_at"`
}

// issueCertificate issues a certificate by hand. Automatic issuance on
// passing is handled inside the assessment service; this endpoint exists
// for corrections, for example a completed course that predates the
// certificate feature.
func (s *Server) issueCertificate(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req issueCertificateRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	learnerID, err := uuid.Parse(strings.TrimSpace(req.LearnerID))
	if err != nil {
		httpx.Fail(w, httpx.FieldError("learner_id", "Choose the learner."))
		return
	}
	courseID, err := uuid.Parse(strings.TrimSpace(req.CourseID))
	if err != nil {
		httpx.Fail(w, httpx.FieldError("course_id", "Choose the course."))
		return
	}
	if _, err := s.db.UserByID(r.Context(), orgID, learnerID); err != nil {
		httpx.Fail(w, httpx.FieldError("learner_id",
			"That learner does not exist in this organisation."))
		return
	}
	if _, err := s.db.CourseByID(r.Context(), orgID, courseID); err != nil {
		httpx.Fail(w, httpx.FieldError("course_id",
			"That course does not exist in this organisation."))
		return
	}
	if req.Score < 0 || req.Score > 100 {
		httpx.Fail(w, httpx.FieldError("score", "The score must be between 0 and 100."))
		return
	}
	var completedAt *time.Time
	if raw := trimOrNil(deref(req.CompletedAt)); raw != nil {
		parsed, err := time.Parse(time.RFC3339, *raw)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("completed_at",
				"Use an RFC 3339 timestamp."))
			return
		}
		completedAt = &parsed
	}
	cert, err := s.svc.IssueCertificate(r.Context(), orgID, actorID, learnerID, courseID,
		req.Score, completedAt)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "certificate.issue", "certificate", cert.ID, map[string]any{
		"learner_id": learnerID, "course_id": courseID, "manual": true,
	})
	httpx.Created(w, s.svc.CertificateShare(cert, s.cfg.BaseURL))
}

func (s *Server) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Revocation is public-facing, so the reason must be recorded for the
	// verification page to explain.
	reason := strings.TrimSpace(req.Reason)
	if len(reason) < 5 {
		httpx.Fail(w, ValidationError("A reason is required.", map[string]any{
			"reason": "Give a short reason, for example 'issued in error'.",
		}))
		return
	}
	cert, err := s.svc.RevokeCertificate(r.Context(), orgID, id, reason)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "certificate.revoke", "certificate", id, map[string]any{"reason": reason})
	httpx.JSON(w, cert)
}

func (s *Server) certificateHTML(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	cert, err := s.db.CertificateByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "certificate"))
		return
	}
	if ClaimsFrom(r.Context()).IsLearner() && cert.LearnerID != actorID {
		httpx.Fail(w, httpx.Forbidden("You can only view your own certificate."))
		return
	}
	html, err := s.svc.RenderCertificateHTML(cert, s.cfg.BaseURL)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// The user is asking to see a certificate, so return a page rather
	// than a download prompt.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src https: data:")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

// ---------------------------------------------------------------------
// Public verification (screen 15) - no authentication required
// ---------------------------------------------------------------------

func (s *Server) publicCertificateJSON(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(chiParam(r, "code"))
	cert, err := s.svc.PublicCertificate(r.Context(), code, s.cfg.BaseURL)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, cert)
}

func (s *Server) publicCertificateHTML(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(chiParam(r, "code"))
	cert, err := s.svc.PublicCertificate(r.Context(), code, s.cfg.BaseURL)
	if err != nil {
		// An invalid or revoked code must not leak a stack trace to a
		// public page.
		httpx.Fail(w, err)
		return
	}
	html, err := s.svc.RenderCertificateHTML(cert, s.cfg.BaseURL)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src https: data:")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

// ---------------------------------------------------------------------
// Dashboards (screen 01)
// ---------------------------------------------------------------------

func (s *Server) dashboardKPIs(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	kpis, err := s.db.DashboardKPIs(r.Context(), orgID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, kpis)
}

func (s *Server) dashboardAttention(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	items, err := s.db.AttentionItems(r.Context(), orgID, 20)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if items == nil {
		items = []domain.AttentionItem{}
	}
	httpx.JSONMeta(w, items, &httpx.Meta{Total: len(items)})
}

func (s *Server) departmentProgress(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var campaignID *uuid.UUID
	if raw := r.URL.Query().Get("campaign_id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			campaignID = &id
		}
	}
	rows, err := s.db.DepartmentProgress(r.Context(), orgID, campaignID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if rows == nil {
		rows = []domain.DepartmentProgress{}
	}
	httpx.JSONMeta(w, rows, &httpx.Meta{Total: len(rows)})
}

// ---------------------------------------------------------------------
// Reports (screens 16, 17, 18)
// ---------------------------------------------------------------------

// reportOverview is the completion summary: totals by status.
func (s *Server) reportOverview(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	f, err := s.assignmentFilterFromQuery(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	breakdown, err := s.db.StatusBreakdown(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, breakdown)
}

// reportRows is the per-learner detail table behind the exports.
func (s *Server) reportRows(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	f, err := s.assignmentFilterFromQuery(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	rows, total, err := s.db.ListAssignments(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if rows == nil {
		rows = []domain.Assignment{}
	}
	httpx.JSONMeta(w, rows, &httpx.Meta{Total: total})
}

func (s *Server) exportReportCSV(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	f, err := s.assignmentFilterFromQuery(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// A manager exports only their own team, whatever the query says.
	if ClaimsFrom(r.Context()).Role == auth.RoleManager {
		f.ManagerID = &actorID
		f.LearnerID = nil
	}
	csv, err := s.db.ReportCSV(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "report.export_csv", "report", orgID, map[string]any{
		"campaign_id": f.CampaignID, "department": f.Department,
	})
	filename := fmt.Sprintf("kaziwise-training-report-%s.csv",
		time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(csv))
}

func (s *Server) exportReportPDF(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	f, err := s.assignmentFilterFromQuery(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if ClaimsFrom(r.Context()).Role == auth.RoleManager {
		f.ManagerID = &actorID
		f.LearnerID = nil
	}
	rows, total, err := s.db.ListAssignments(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	html, err := renderReportHTML(s.db, orgID, rows, total)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "report.export_pdf", "report", orgID, map[string]any{
		"campaign_id": f.CampaignID, "rows": total,
	})
	// The prototype ships an HTML document that prints to PDF; the client
	// offers it as a download rather than pretending it is a real PDF.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="kaziwise-training-report.html"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

func (s *Server) exportReportXLSX(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	f, err := s.assignmentFilterFromQuery(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if ClaimsFrom(r.Context()).Role == auth.RoleManager {
		f.ManagerID = &actorID
		f.LearnerID = nil
	}
	rows, total, err := s.db.ListAssignments(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	html, err := renderReportHTML(s.db, orgID, rows, total)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "report.export_xlsx", "report", orgID, map[string]any{
		"campaign_id": f.CampaignID, "rows": total,
	})
	// A real .xlsx is a zip of XML parts. Rather than hand-roll that, the
	// prototype serves a spreadsheet-ready HTML table with the tab-separated
	// MIME type used by Excel's "open as web page" import.
	w.Header().Set("Content-Type", "application/vnd.ms-excel; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="kaziwise-training-report.xls"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

// assignmentFilterFromQuery builds a report filter, rejecting an
// unparseable id rather than silently ignoring it.
func (s *Server) assignmentFilterFromQuery(r *http.Request) (store.AssignmentFilter, error) {
	q := r.URL.Query()
	f := store.AssignmentFilter{
		Status: q.Get("status"), Department: q.Get("department"),
		Search: q.Get("q"), From: trimOrNil(q.Get("from")), To: trimOrNil(q.Get("to")),
		Page: 1, PerPage: 1000,
	}
	for param, target := range map[string]**uuid.UUID{
		"campaign_id": &f.CampaignID, "course_id": &f.CourseID, "learner_id": &f.LearnerID,
	} {
		raw := strings.TrimSpace(q.Get(param))
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return f, httpx.FieldError(param, "That is not a valid id.")
		}
		*target = &id
	}
	// from/to are date filters for the report; an unparseable value would
	// otherwise produce a wrong report rather than an error.
	for _, name := range []string{"from", "to"} {
		raw := strings.TrimSpace(q.Get(name))
		if raw == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return f, httpx.FieldError(name, "Use the date format 2006-01-02.")
		}
	}
	return f, nil
}

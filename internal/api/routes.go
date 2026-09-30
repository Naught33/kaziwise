package api

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/auth"

	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// routeEntry is one method and path the router serves.
type routeEntry struct {
	Method string
	Path   string
}

// chiMethodAll is the key chi stores its catch-all mount handler under. It
// is never a real endpoint, so route collection ignores it.
const chiMethodAll = "*"

// routeIndex walks the assembled router and returns every method + path
// pair in a stable order. The API index is built from this rather than a
// hand-maintained list, so it cannot drift away from the real routes.
func (s *Server) routeIndex() []routeEntry {
	routes, ok := s.router.(chi.Routes)
	if !ok {
		return nil
	}
	var out []routeEntry
	collectRoutes(routes, "", &out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// chi reports a sub-router's pattern relative to its parent, so the
// prefixes are joined and duplicate separators collapsed.
//
// A node can carry both its own handlers and sub-routes, so the handlers
// are recorded first and the recursion is not short-circuited.
//
// A mounted group is reported with a "*" handler: that is chi's catch-all
// for the mount path, not an endpoint, so it is skipped. A concrete method
// registered on a mount path is reported on the same node, and the node's
// pattern is the mount path with its wildcard trimmed, so it comes out as
// the bare path.
func collectRoutes(r chi.Routes, prefix string, out *[]routeEntry) {
	for _, rt := range r.Routes() {
		full := cleanPath(prefix + strings.TrimSuffix(rt.Pattern, "*"))
		for method := range rt.Handlers {
			if method == chiMethodAll {
				continue
			}
			*out = append(*out, routeEntry{Method: method, Path: full})
		}
		if rt.SubRoutes != nil {
			collectRoutes(rt.SubRoutes, full, out)
		}
	}
}

func cleanPath(p string) string {
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

func isConflict(err error) bool { return errors.Is(err, store.ErrConflict) }

// pathUUID reads and validates a UUID path parameter.
func pathUUID(r *http.Request, key string) (uuid.UUID, error) {
	raw := chi.URLParam(r, key)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httpx.FieldError(key, "This identifier is not a valid UUID.")
	}
	return id, nil
}

// mustUUID is for handlers where a bad id is the caller's only problem.
func mustUUID(r *http.Request, key string) uuid.UUID {
	id, err := pathUUID(r, key)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// registerRoutes mounts every authenticated endpoint group. The
// unauthenticated routes live in authRoutes and the public certificate
// routes in server.go.
func (s *Server) registerRoutes(r chi.Router) {
	// --- Session and self ---------------------------------------------
	// refresh and forgot-password are registered as public routes in
	// authRoutes: they are the two calls a client makes precisely when it
	// has no usable access token.
	r.Get("/me", s.me)
	r.Post("/logout", s.logout)
	r.Post("/change-password", s.changePassword)

	// --- Organisation (screen 02) --------------------------------------
	r.Get("/org", s.getOrg)
	r.Patch("/org", requireRoleFunc(super, staff)(s.updateOrg))
	r.Get("/org/departments", s.listDepartments)
	r.Get("/org/team", requireRoleFunc(manage)(s.team))

	// --- Employees (screen 02) ----------------------------------------
	r.Route("/employees", func(e chi.Router) {
		e.Get("/", s.listUsers)
		e.Get("/{id}", s.getUser)
		e.Post("/", requireRoleFunc(super, staff)(s.createUser))
		e.Patch("/{id}", requireRoleFunc(super, staff)(s.updateUser))
		e.Delete("/{id}", requireRoleFunc(super, staff)(s.deleteUser))
		e.Post("/import", requireRoleFunc(super, staff)(s.importUsers))
		e.Post("/invite", requireRoleFunc(super, staff)(s.createInvite))
	})

	// --- Courses (screen 03) ------------------------------------------
	r.Route("/courses", func(c chi.Router) {
		c.Get("/", s.listCourses)
		c.Get("/{id}", s.getCourse)
		c.Get("/{id}/outline", s.courseOutline)
		c.Post("/", requireRoleFunc(super, staff)(s.createCourse))
		c.Patch("/{id}", requireRoleFunc(super, staff)(s.updateCourse))
		c.Delete("/{id}", requireRoleFunc(super, staff)(s.deleteCourse))
		c.Post("/{id}/publish", requireRoleFunc(super, staff)(s.publishCourse))
		c.Post("/{id}/unpublish", requireRoleFunc(super, staff)(s.unpublishCourse))

		// Builder: modules, lessons and content blocks. The path parameter
		// names match what each handler reads, so a renamed segment fails
		// the route table test instead of failing at runtime.
		c.Post("/{id}/modules", requireRoleFunc(super, staff)(s.createModule))
		c.Post("/{id}/modules/reorder", requireRoleFunc(super, staff)(s.reorderModules))
		c.Post("/modules/{moduleId}/lessons/reorder", requireRoleFunc(super, staff)(s.reorderLessons))
		c.Get("/{id}/modules/{moduleId}/lessons", s.listLessons)
		c.Post("/{id}/modules/{moduleId}/lessons", requireRoleFunc(super, staff)(s.createLesson))
		c.Patch("/{id}/modules/{moduleId}", requireRoleFunc(super, staff)(s.updateModule))
		c.Delete("/{id}/modules/{moduleId}", requireRoleFunc(super, staff)(s.deleteModule))

		c.Patch("/lessons/{id}", requireRoleFunc(super, staff)(s.updateLesson))
		c.Delete("/lessons/{id}", requireRoleFunc(super, staff)(s.deleteLesson))
		c.Get("/lessons/{id}/blocks", s.listBlocks)
		c.Post("/lessons/{id}/blocks", requireRoleFunc(super, staff)(s.createBlock))
		c.Post("/lessons/{id}/blocks/reorder", requireRoleFunc(super, staff)(s.reorderBlocks))
		// Questions are authored against a lesson: the builder screen reads
		// and writes this collection, so the POST has to sit beside the GET
		// or chi answers 405 on a path it already serves.
		c.Get("/lessons/{id}/questions", requireRoleFunc(manage)(s.listLessonQuestions))
		c.Post("/lessons/{id}/questions", requireRoleFunc(super, staff)(s.createLessonQuestion))
		c.Patch("/blocks/{id}", requireRoleFunc(super, staff)(s.updateBlock))
		c.Delete("/blocks/{id}", requireRoleFunc(super, staff)(s.deleteBlock))

		// The course player, reachable by the assigned learner. The page
		// view routes are keyed on the block, which is the row being
		// recorded, and the progress route needs both the course and the
		// lesson to match the learner's assignment.
		c.Get("/{id}/play", s.playCourse)
		c.Post("/blocks/{id}/pages", s.recordPageView)
		c.Get("/blocks/{id}/pages", s.pageViewProgress)
		c.Get("/{courseId}/lessons/{id}/progress", s.lessonProgress)

		// Question bank.
		c.Get("/{id}/questions", requireRoleFunc(manage)(s.listQuestions))
		c.Post("/{id}/questions", requireRoleFunc(super, staff)(s.createQuestion))
		c.Post("/{id}/questions/reorder", requireRoleFunc(super, staff)(s.reorderQuestions))
		c.Patch("/questions/{questionId}", requireRoleFunc(super, staff)(s.updateQuestion))
		c.Delete("/questions/{questionId}", requireRoleFunc(super, staff)(s.deleteQuestion))
	})

	// --- Assets (screen 04) -------------------------------------------
	r.Route("/assets", func(a chi.Router) {
		a.Use(requireRole(manage))
		a.Get("/", s.listAssets)
		a.Post("/upload", s.uploadAsset)
		a.Get("/{id}", s.getAsset)
		a.Get("/{id}/pages", s.assetPages)
		a.Delete("/{id}", s.deleteAsset)
	})

	// --- Campaigns and assignments (screen 05) -----------------------
	r.Route("/campaigns", func(c chi.Router) {
		c.Use(requireRole(manage))
		c.Get("/", s.listCampaigns)
		c.Get("/{id}", s.getCampaign)
		c.Get("/{id}/audience", s.campaignAudience)
		c.Get("/{id}/remind", s.listReminders)
		c.Post("/", requireRoleFunc(super, staff)(s.createCampaign))
		c.Patch("/{id}", requireRoleFunc(super, staff)(s.updateCampaign))
		c.Delete("/{id}", requireRoleFunc(super, staff)(s.deleteCampaign))
		c.Post("/{id}/launch", requireRoleFunc(super, staff)(s.launchCampaign))
		c.Post("/{id}/close", requireRoleFunc(super, staff)(s.closeCampaign))
		c.Post("/{id}/remind", s.remindCampaign)
	})
	r.Route("/assignments", func(a chi.Router) {
		a.Get("/", s.listAssignments)
		a.Get("/{id}", s.getAssignment)
		a.Get("/{id}/remind", s.listReminders)
		a.Post("/{id}/remind", requireRoleFunc(manage)(s.remindAssignment))
	})

	// --- Learner (screens 08, 09) -------------------------------------
	r.Get("/dashboard", s.dashboard)
	r.Get("/learners", requireRoleFunc(manage)(s.listLearners))
	r.Get("/learners/{id}", requireRoleFunc(manage)(s.getLearner))
	r.Get("/learners/{id}/summary", requireRoleFunc(manage)(s.getLearnerSummary))
	r.Get("/learners/{id}/certificates", s.getLearnerCertificates)
	r.Get("/me/training", s.myTraining)
	r.Get("/me/attempts", learnerOnly(s.myAttempts))
	r.Get("/me/certificates", s.getLearnerCertificates)
	r.Post("/me/lessons/{id}/start", learnerOnly(s.startLesson))
	r.Post("/me/lessons/{id}/complete", learnerOnly(s.completeLesson))

	// --- Assessment (screens 10, 11, 12) -----------------------------
	r.Post("/assignments/{assignmentId}/attempts", learnerOnly(s.startAttempt))
	r.Get("/attempts", s.listAttempts)
	r.Post("/attempts/{attemptId}/answers", learnerOnly(s.saveAnswers))
	r.Post("/attempts/{attemptId}/submit", learnerOnly(s.submitAttempt))
	r.Get("/attempts/{attemptId}/result", s.attemptResult)
	r.Get("/attempts/{attemptId}/paper", learnerOnly(s.attemptPaper))
	r.Get("/attempts/{attemptId}/answers", s.attemptAnswers)
	r.Post("/attempts/{attemptId}/grade", requireRoleFunc(manage)(s.gradeAnswers))
	r.Post("/attempts/{attemptId}/grade-answer", requireRoleFunc(manage)(s.gradeAnswer))

	// --- Grading queue (screen 13) -----------------------------------
	r.Get("/grading/pending", requireRoleFunc(manage)(s.listPendingGrading))

	// --- Dashboards and reports (screens 01, 16, 17, 18) -------------
	r.Get("/reports/overview", requireRoleFunc(manage)(s.reportOverview))
	r.Get("/reports/rows", requireRoleFunc(manage)(s.reportRows))
	r.Get("/reports/departments", requireRoleFunc(manage)(s.departmentProgress))
	r.Get("/reports/export/csv", requireRoleFunc(manage)(s.exportReportCSV))
	r.Get("/reports/export/pdf", requireRoleFunc(manage)(s.exportReportPDF))
	r.Get("/reports/export/xlsx", requireRoleFunc(manage)(s.exportReportXLSX))
	r.Get("/dashboard/kpis", requireRoleFunc(manage)(s.dashboardKPIs))
	r.Get("/dashboard/attention", requireRoleFunc(manage)(s.dashboardAttention))
	// The staff dashboard, distinct from the learner /dashboard above.
	r.Get("/dashboard/admin", requireRoleFunc(manage)(s.adminDashboard))

	// --- Certificates (screens 14, 15) -------------------------------
	r.Route("/certificates", func(c chi.Router) {
		c.Get("/", s.listCertificates)
		c.Get("/{id}", s.getCertificate)
		c.Get("/{id}/html", s.certificateHTML)
		c.Post("/", requireRoleFunc(super, staff)(s.issueCertificate))
		c.Post("/{id}/revoke", requireRoleFunc(super, staff)(s.revokeCertificate))
	})

	// --- Audit ---------------------------------------------------------
	r.Get("/audit", requireRoleFunc(super, staff)(s.listAudit))
}

// learnerOnly allows learners through; staff are not learners and are
// refused so they use the reporting screens instead.
func learnerOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := ClaimsFrom(r.Context())
		if c == nil || c.Role != auth.RoleLearner {
			httpx.Fail(w, httpx.NewError(http.StatusForbidden, "learner_only",
				"This endpoint is for learner accounts."))
			return
		}
		h(w, r)
	}
}

// orgAndActor resolves the tenant and the acting user for a request.
func orgAndActor(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	c := ClaimsFrom(r.Context())
	if c == nil {
		return uuid.Nil, uuid.Nil, httpx.Unauthorized("An access token is required.")
	}
	if c.OrgID == uuid.Nil {
		return uuid.Nil, uuid.Nil, httpx.Forbidden("Your account is not linked to an organisation.")
	}
	return c.OrgID, c.UserID, nil
}

// clientIP reports the caller's address, honouring the forwarded header
// only when a proxy is trusted. The result is a pointer because the audit
// table stores NULL for a system-initiated action.
func (s *Server) clientIP(r *http.Request) *string {
	raw := ""
	if len(s.cfg.TrustedProxies) > 0 {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			raw = strings.TrimSpace(strings.Split(fwd, ",")[0])
		}
	}
	if raw == "" {
		// SplitHostPort rather than Split(":") so an IPv6 remote address
		// does not become the truncated string "2001:db8".
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			raw = host
		} else {
			raw = r.RemoteAddr
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return &raw
}

func (s *Server) userAgent(r *http.Request) *string {
	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if ua == "" {
		return nil
	}
	if len(ua) > 300 {
		ua = ua[:300]
	}
	return &ua
}

// entityRef renders a uuid for the audit trail's text column. The nil uuid
// is stored as NULL rather than as the all-zero string, so "no entity" and
// "entity 00000000-0000-0000-0000-000000000000" stay distinguishable.
func entityRef(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	s := id.String()
	return &s
}

// audit records an action without ever failing the request.
func (s *Server) audit(r *http.Request, action, entity string, entityID uuid.UUID, meta map[string]any) {
	orgID, actorID, _ := orgAndActor(r)
	s.writeAudit(r, store.AuditParams{
		OrgID: &orgID, ActorID: &actorID, Action: action, Entity: entity,
		EntityID: entityRef(entityID), Meta: meta,
		IP: s.clientIP(r), UserAgent: s.userAgent(r),
	})
}

// auditSystem records an action from an unauthenticated endpoint (login,
// registration), where there are no claims to attribute it to yet. The org
// is taken from the profile that was just created, so the trail is not
// orphaned.
func (s *Server) auditSystem(r *http.Request, action, entity string, entityID uuid.UUID, meta map[string]any) {
	p := store.AuditParams{
		Action: action, Entity: entity, EntityID: entityRef(entityID),
		Meta: meta, IP: s.clientIP(r), UserAgent: s.userAgent(r),
	}
	if entityID != uuid.Nil {
		var orgID uuid.UUID
		if err := s.db.Pool().QueryRow(r.Context(),
			`select org_id from profiles where id = $1`, entityID).Scan(&orgID); err == nil {
			p.OrgID = &orgID
		}
	}
	s.writeAudit(r, p)
}

func (s *Server) writeAudit(r *http.Request, p store.AuditParams) {
	s.db.Audit(r.Context(), p)
}

// trimOrNil returns a trimmed string pointer, or nil when empty, so an
// omitted field stays NULL rather than becoming an empty string.
func trimOrNil(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

// ValidationError builds a 422 with per-field messages for the front end.
func ValidationError(msg string, fields map[string]any) error {
	return httpx.Unprocessable(msg).WithFields(fields)
}

// notFoundErr renders a consistent 404 for a named resource.
func notFoundErr(what string) error {
	return httpx.NewError(http.StatusNotFound, what+"_not_found", capitalise(what)+" not found.")
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// crypto/rand helpers for temporary passwords. crypto/rand is used
// rather than math/rand because these credentials are handed to real
// users.
func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failure of the system entropy source is unrecoverable for
		// credential generation; fail closed rather than weaken the
		// password.
		panic("kaziwise: crypto/rand unavailable: " + err.Error())
	}
	return int(binary.BigEndian.Uint32(b[:]) % uint32(n))
}

// statusOf maps a store error onto an application error.
func statusOf(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case isNotFound(err):
		return notFoundErr(what)
	case isConflict(err):
		return httpx.Conflict(what+"_conflict", err.Error())
	default:
		return err
	}
}

// chiParam reads a raw path parameter, used by the public routes where
// the value is not a UUID.
func chiParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

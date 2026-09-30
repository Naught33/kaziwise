package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/config"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
)

// chi panics at construction time on a duplicate or conflicting route, so
// building the table in a test is a real check that every path is unique
// and that no pattern shadows another.
func TestRouteTableBuildsWithoutConflict(t *testing.T) {
	if testServer(t).Router() == nil {
		t.Fatal("Router() returned nil")
	}
}

func TestRouteTableCoversTheScreens(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range testServer(t).routeIndex() {
		seen[e.Method+" "+e.Path] = true
	}

	// The endpoints the specification calls out by name. Each one is a
	// screen in the prototype, so a rename that breaks a screen fails here.
	want := []string{
		"GET /health",
		"GET /ready",
		"POST /v1/auth/register",
		"POST /v1/auth/login",
		"POST /v1/auth/orgs/resolve",
		"POST /v1/auth/accept-invite",
		"POST /v1/auth/refresh",
		"POST /v1/auth/forgot-password",
		"POST /v1/logout",
		"POST /v1/change-password",
		"GET /v1/me",
		"GET /v1/me/training",
		"GET /v1/me/attempts",
		"GET /v1/me/certificates",
		"POST /v1/me/lessons/{id}/start",
		"POST /v1/me/lessons/{id}/complete",
		"GET /v1/org",
		"PATCH /v1/org",
		"GET /v1/employees",
		"POST /v1/employees",
		"GET /v1/employees/{id}",
		"PATCH /v1/employees/{id}",
		"DELETE /v1/employees/{id}",
		"POST /v1/employees/import",
		"POST /v1/employees/invite",
		"GET /v1/org/team",
		"GET /v1/org/departments",
		"GET /v1/courses",
		"POST /v1/courses",
		"GET /v1/courses/{id}",
		"PATCH /v1/courses/{id}",
		"DELETE /v1/courses/{id}",
		"GET /v1/courses/{id}/outline",
		"POST /v1/courses/{id}/publish",
		"POST /v1/courses/{id}/unpublish",
		"POST /v1/courses/{id}/modules",
		"PATCH /v1/courses/{id}/modules/{moduleId}",
		"POST /v1/courses/{id}/modules/{moduleId}/lessons",
		"GET /v1/courses/{id}/modules/{moduleId}/lessons",
		"GET /v1/courses/lessons/{id}/questions",
		"POST /v1/courses/lessons/{id}/blocks",
		"GET /v1/courses/lessons/{id}/blocks",
		"POST /v1/courses/{id}/modules/reorder",
		"POST /v1/courses/modules/{moduleId}/lessons/reorder",
		"GET /v1/courses/{courseId}/lessons/{id}/progress",
		"POST /v1/assets/upload",
		"GET /v1/assets/{id}/pages",
		"GET /v1/campaigns",
		"POST /v1/campaigns",
		"POST /v1/campaigns/{id}/launch",
		"GET /v1/campaigns/{id}/audience",
		"POST /v1/assignments/{assignmentId}/attempts",
		"POST /v1/attempts/{attemptId}/answers",
		"POST /v1/attempts/{attemptId}/submit",
		"GET /v1/attempts/{attemptId}/result",
		"GET /v1/grading/pending",
		"POST /v1/attempts/{attemptId}/grade",
		"POST /v1/attempts/{attemptId}/grade-answer",
		"GET /v1/learners",
		"GET /v1/learners/{id}/summary",
		"GET /v1/courses/{id}/play",
		"GET /v1/dashboard/kpis",
		"GET /v1/reports/overview",
		"GET /v1/reports/export/csv",
		"GET /v1/reports/export/pdf",
		"GET /v1/reports/export/xlsx",
		"GET /v1/certificates",
		"GET /v1/certificates/{id}/html",
		"POST /v1/certificates/{id}/revoke",
		"GET /v1/certificates/public/{code}",
		"GET /v1/audit",
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("route %q is not registered", w)
		}
	}
}

// TestRoutePatternsAreUnique guards against two handlers silently claiming
// the same method and path, which chi allows when they live in different
// sub-routers.
func TestRoutePatternsAreUnique(t *testing.T) {
	seen := map[string]int{}
	for _, e := range testServer(t).routeIndex() {
		seen[e.Method+" "+e.Path]++
	}
	for r, n := range seen {
		if n > 1 {
			t.Errorf("route %q is registered %d times", r, n)
		}
	}
}

// TestEveryRouteSitsUnderTheVersionPrefix is a cheap guard against a
// handler being added to the root router by mistake, which would publish
// it without the API contract. The bare /v1 is the API index itself, so
// it is the one route that legitimately has no trailing path.
func TestEveryRouteSitsUnderTheVersionPrefix(t *testing.T) {
	for _, e := range testServer(t).routeIndex() {
		switch {
		case e.Path == "/health", e.Path == "/ready", e.Path == "/version",
			e.Path == "/metrics":
		case e.Path == "/v1":
		case strings.HasPrefix(e.Path, "/v1/"):
		default:
			t.Errorf("route %s %s is outside the /v1 prefix", e.Method, e.Path)
		}
	}
}

// The index route is mounted on the same path as the /v1 group, and chi
// replaces a handler registered on a mount path with its own catch-all.
// The API would 404 on its own entry point if the registration order were
// wrong, so this drives a real request rather than trusting the table.
func TestRouteIndexIsServedAndDescribesItself(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1 returned %d, want %d; body %s",
			w.Code, http.StatusOK, w.Body.String())
	}

	var env struct {
		Data struct {
			Endpoints []struct {
				Method string `json:"method"`
				Path   string `json:"path"`
				Auth   string `json:"auth"`
			} `json:"endpoints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not the expected envelope: %v", err)
	}
	if len(env.Data.Endpoints) == 0 {
		t.Fatal("the served index lists no endpoints")
	}

	found := false
	for _, e := range env.Data.Endpoints {
		if e.Method == http.MethodGet && e.Path == "/v1" {
			found = true
			if e.Auth != "none" {
				t.Errorf("GET /v1 is labelled %q, want none", e.Auth)
			}
		}
	}
	if !found {
		t.Error("the served index does not list GET /v1")
	}
}

// testServer builds a Server with no database. Handlers are never invoked
// in these tests, so the nil pool is safe.
func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{AppName: "kaziwise-api", BaseURL: "https://api.test"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, nil, nil, nil)
}

func TestValidateRoleRejectsUnknownRoles(t *testing.T) {
	for _, ok := range []string{"super_admin", "org_admin", "manager", "learner"} {
		if err := validateRole(ok); err != nil {
			t.Errorf("validateRole(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "admin", "Learner", "root", "org admin"} {
		if err := validateRole(bad); err == nil {
			t.Errorf("validateRole(%q) = nil, want an error", bad)
		}
	}
}

// The route table is written as super/staff for authoring and manage for
// reading, so the permission check has to nest: an org admin must be able
// to read reports and campaigns, and a super admin must be able to author.
func TestRoleAllowedNestsTheStaffLadder(t *testing.T) {
	cases := []struct {
		role    string
		allowed []string
		want    int
	}{
		{auth.RoleSuperAdmin, []string{super, staff}, http.StatusOK},
		{auth.RoleOrgAdmin, []string{super, staff}, http.StatusOK},
		{auth.RoleManager, []string{super, staff}, http.StatusForbidden},
		{auth.RoleLearner, []string{super, staff}, http.StatusForbidden},
		{auth.RoleManager, []string{manage}, http.StatusOK},
		{auth.RoleOrgAdmin, []string{manage}, http.StatusOK},
		{auth.RoleSuperAdmin, []string{manage}, http.StatusOK},
		{auth.RoleLearner, []string{manage}, http.StatusForbidden},
		{auth.RoleSuperAdmin, []string{auth.RoleLearner}, http.StatusForbidden},
		{auth.RoleLearner, []string{auth.RoleLearner}, http.StatusForbidden},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/v1/reports/overview", nil)
		r = r.WithContext(context.WithValue(r.Context(), claimsKey,
			&auth.Claims{UserID: uuid.New(), Role: auth.Role(tc.role)}))
		w := httptest.NewRecorder()

		// The next handler is only reached when the check passes, so the
		// status alone tells us whether the role was admitted.
		gate := requireRoleFunc(tc.allowed...)(func(http.ResponseWriter, *http.Request) {
			// no-op: reaching here means the role passed.
		})
		gate.ServeHTTP(w, r)

		if w.Code != tc.want {
			t.Errorf("role %q against %v: status %d, want %d",
				tc.role, tc.allowed, w.Code, tc.want)
		}
	}
}

// A request with no claims at all is refused before the roles are compared.
func TestRoleAllowedRejectsAnAnonymousRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/reports/overview", nil)
	w := httptest.NewRecorder()

	gate := requireRoleFunc(manage)(func(http.ResponseWriter, *http.Request) {})
	gate.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestGeneratePasswordSatisfiesThePolicy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw := generatePassword()
		if err := validatePassword(pw); err != nil {
			t.Fatalf("generated password %q fails the policy: %v", pw, err)
		}
		if len(pw) != 12 {
			t.Fatalf("generated password %q has length %d, want 12", pw, len(pw))
		}
		// Ambiguous glyphs are excluded so a password can be read aloud.
		for _, bad := range "0O1lI" {
			if strings.ContainsRune(pw, bad) {
				t.Fatalf("generated password %q contains the ambiguous character %q", pw, bad)
			}
		}
		seen[pw] = true
	}
	// 200 draws from a 60 character alphabet: a collision would mean the
	// generator is not shuffling.
	if len(seen) < 199 {
		t.Fatalf("only %d distinct passwords out of 200, the generator looks weak", len(seen))
	}
}

func TestShufflePermutesWithoutLosingElements(t *testing.T) {
	in := []byte("abcdefghijkl")
	before := sortString(string(append([]byte(nil), in...)))
	shuffle(in)
	// The order is allowed to change; the multiset is not. Comparing the
	// sorted copies catches both a dropped and a duplicated element.
	if after := sortString(string(in)); before != after {
		t.Fatalf("shuffle changed the elements: %q became %q", before, after)
	}
}

// sortString returns the bytes of s in ascending order. The input here is
// ASCII, so byte order and character order agree.
func sortString(s string) string {
	b := []byte(s)
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	return string(b)
}

func TestValidateOptionsEnforcesTheChoiceRules(t *testing.T) {
	tests := []struct {
		name    string
		kind    domain.QuestionType
		in      []optionRequest
		wantErr string
	}{
		{
			name: "single choice with one correct answer is valid",
			kind: domain.QSingleChoice,
			in: []optionRequest{
				{Label: "Yes", IsCorrect: true}, {Label: "No"},
			},
		},
		{
			name:    "single choice with two correct answers is rejected",
			kind:    domain.QSingleChoice,
			in:      []optionRequest{{Label: "A", IsCorrect: true}, {Label: "B", IsCorrect: true}},
			wantErr: "exactly one correct",
		},
		{
			name:    "single choice with no correct answer is rejected",
			kind:    domain.QSingleChoice,
			in:      []optionRequest{{Label: "A"}, {Label: "B"}},
			wantErr: "exactly one correct",
		},
		{
			name:    "a choice question needs at least two options",
			kind:    domain.QSingleChoice,
			in:      []optionRequest{{Label: "Only", IsCorrect: true}},
			wantErr: "at least two options",
		},
		{
			name:    "duplicate option labels are rejected",
			kind:    domain.QMultiChoice,
			in:      []optionRequest{{Label: "Same", IsCorrect: true}, {Label: "same"}},
			wantErr: "must be unique",
		},
		{
			name:    "an empty option label is rejected",
			kind:    domain.QMultiChoice,
			in:      []optionRequest{{Label: "  ", IsCorrect: true}, {Label: "B"}},
			wantErr: "needs a label",
		},
		{
			name:    "multi choice needs at least one correct answer",
			kind:    domain.QMultiChoice,
			in:      []optionRequest{{Label: "A"}, {Label: "B"}},
			wantErr: "at least one correct",
		},
		{
			name:    "multi choice where everything is correct is rejected",
			kind:    domain.QMultiChoice,
			in:      []optionRequest{{Label: "A", IsCorrect: true}, {Label: "B", IsCorrect: true}},
			wantErr: "meaningless",
		},
		{
			name:    "a written question must not carry options",
			kind:    domain.QLongText,
			in:      []optionRequest{{Label: "A"}},
			wantErr: "must not define options",
		},
		{
			name: "a written question without options is valid",
			kind: domain.QShortText,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateOptions(tc.kind, tc.in)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("got error %v, want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("got nil, want an error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateOptionsKeepsTheCorrectFlags(t *testing.T) {
	out, err := validateOptions(domain.QMultiChoice, []optionRequest{
		{Label: "A", IsCorrect: true}, {Label: "B"}, {Label: "C", IsCorrect: true},
	})
	if err != nil {
		t.Fatalf("validateOptions: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d options, want 3", len(out))
	}
	correct := 0
	for _, o := range out {
		if o.IsCorrect {
			correct++
		}
	}
	if correct != 2 {
		t.Fatalf("got %d correct options, want 2", correct)
	}
}

func TestValidateTextLimits(t *testing.T) {
	intp := func(v int) *int { return &v }
	tests := []struct {
		name    string
		kind    domain.QuestionType
		min     *int
		max     *int
		wantErr string
	}{
		{
			name:    "a length limit on a choice question is rejected",
			kind:    domain.QSingleChoice,
			min:     intp(5),
			wantErr: "only apply to written",
		},
		{
			name: "a short answer with a range is valid",
			kind: domain.QShortText,
			min:  intp(2),
			max:  intp(50),
		},
		{
			name:    "min above max is rejected",
			kind:    domain.QShortText,
			min:     intp(9),
			max:     intp(2),
			wantErr: "cannot exceed",
		},
		{
			name:    "a negative minimum is rejected",
			kind:    domain.QLongText,
			min:     intp(-1),
			wantErr: "negative",
		},
		{
			name:    "a zero maximum is rejected",
			kind:    domain.QLongText,
			max:     intp(0),
			wantErr: "greater than zero",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTextLimits(tc.kind, tc.min, tc.max)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("got error %v, want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("got nil, want an error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateQuestionType(t *testing.T) {
	for _, ok := range []domain.QuestionType{
		domain.QSingleChoice, domain.QMultiChoice, domain.QShortText, domain.QLongText,
	} {
		if err := validateQuestionType(ok); err != nil {
			t.Errorf("validateQuestionType(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []domain.QuestionType{"", "essay", "true_false", "MCQ"} {
		if err := validateQuestionType(bad); err == nil {
			t.Errorf("validateQuestionType(%q) = nil, want an error", bad)
		}
	}
}

func TestReportTemplateEscapesLearnerNames(t *testing.T) {
	rows := []domain.Assignment{{
		LearnerName:  `<script>alert(1)</script>`,
		LearnerEmail: "a@b.test",
		CampaignName: "Safety",
		CourseTitle:  "Fire drill",
		Status:       domain.AssignPassed,
		ProgressPct:  100,
	}}
	// The organisation name is the injection vector the org admin controls;
	// the learner name is the one a manager types in.
	var sb strings.Builder
	if err := reportTemplate.Execute(&sb, reportView{
		OrgName: "<b>Acme</b>", GeneratedAt: "now", Total: 1, Rows: rows,
	}); err != nil {
		t.Fatalf("render report: %v", err)
	}
	out := sb.String()
	if strings.Contains(out, "<script>") {
		t.Error("the learner name was rendered as raw markup")
	}
	if strings.Contains(out, "<b>Acme</b>") {
		t.Error("the organisation name was rendered as raw markup")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("the learner name is missing from the report, escaped or otherwise")
	}
}

func TestEntityRefKeepsNilDistinct(t *testing.T) {
	if got := entityRef(uuid.Nil); got != nil {
		t.Fatalf("entityRef(nil uuid) = %q, want nil", *got)
	}
	id := uuid.New()
	got := entityRef(id)
	if got == nil || *got != id.String() {
		t.Fatalf("entityRef(%s) = %v, want %q", id, got, id.String())
	}
}

func TestClientIPHandlesIPv6(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[2001:db8::1]:54321"
	if got := srv.clientIP(r); got == nil || *got != "2001:db8::1" {
		t.Fatalf("clientIP = %v, want 2001:db8::1", got)
	}
}

func TestUserAgentIsBoundedAndNeverEmpty(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := srv.userAgent(r); got != nil {
		t.Fatalf("userAgent with no header = %v, want nil", *got)
	}
	r.Header.Set("User-Agent", strings.Repeat("x", 400))
	got := srv.userAgent(r)
	if got == nil || len(*got) != 300 {
		t.Fatalf("userAgent was not truncated to 300 characters: %v", got)
	}
}

func TestReadAllLimitedRejectsAnOversizedBody(t *testing.T) {
	// A body past the limit must fail loudly. Returning a truncated CSV
	// would import a partial roster and report it as a success.
	_, err := readAllLimited(strings.NewReader(strings.Repeat("a,b\n", 64)), 16)
	var appErr *httpx.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("oversized body returned %v, want an *httpx.Error", err)
	}
	if appErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", appErr.Status)
	}
}

func TestReadAllLimitedKeepsABodyAtTheLimit(t *testing.T) {
	want := "email,role\nn@example.com,learner\n"
	got, err := readAllLimited(strings.NewReader(want), int64(len(want)))
	if err != nil {
		t.Fatalf("a body exactly at the limit failed: %v", err)
	}
	if string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReadAllLimitedRejectsAnEmptyBody(t *testing.T) {
	_, err := readAllLimited(strings.NewReader(""), 16)
	var appErr *httpx.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("empty body returned %v, want an *httpx.Error", err)
	}
	if appErr.Fields["file"] == nil {
		t.Fatalf("fields = %v, want a message on file", appErr.Fields)
	}
}

func TestAmbiguousOrgErrorNamesEveryCandidate(t *testing.T) {
	err := &ambiguousOrgError{slugs: []string{"acme", "globex"}}
	if !strings.Contains(err.Error(), "acme") || !strings.Contains(err.Error(), "globex") {
		t.Fatalf("message %q does not list both organisations", err.Error())
	}
	// It has to be matchable by errors.As so login can answer 409 instead
	// of leaking a generic 404.
	var target *ambiguousOrgError
	if !errors.As(error(err), &target) || len(target.slugs) != 2 {
		t.Fatalf("errors.As did not recover the candidate list: %v", target)
	}
}

func TestValidationErrorCarriesFieldMessages(t *testing.T) {
	err := ValidationError("fix the fields", map[string]any{"title": "required"})
	var appErr *httpx.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("ValidationError returned %T, want *httpx.Error", err)
	}
	if appErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", appErr.Status)
	}
	if appErr.Fields["title"] != "required" {
		t.Fatalf("fields = %v, want the title message", appErr.Fields)
	}
}

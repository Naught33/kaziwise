package store

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestListBuilderArgNumbering pins the placeholder contract that
// orgScope + add() rely on: the org clause owns $1 and each argument takes
// the next $N.
func TestListBuilderArgNumbering(t *testing.T) {
	b := orgScope("org-1")
	if got, want := b.arg(), "$2"; got != want {
		t.Fatalf("arg after org scope = %q, want %q", got, want)
	}
	b.add("p.role = "+b.arg(), "learner")
	if got, want := b.arg(), "$3"; got != want {
		t.Fatalf("arg after one clause = %q, want %q", got, want)
	}
	b.add("p.status = "+b.arg(), "active")
	if got, want := b.whereClause(), " where org_id = $1 and p.role = $2 and p.status = $3"; got != want {
		t.Errorf("whereClause() = %q, want %q", got, want)
	}
	if got, want := len(b.values()), 3; got != want {
		t.Errorf("arg count = %d, want %d", got, want)
	}
}

// TestListBuilderPlaceholderReuse documents the deliberate reuse case: one
// value tested against three columns shares a single placeholder and
// therefore a single argument. Reading arg() more than once in one clause
// string is what makes this work.
func TestListBuilderPlaceholderReuse(t *testing.T) {
	b := orgScope("org-1")
	like := "%ops%"
	b.add("(p.full_name ilike "+b.arg()+" or p.email ilike "+b.arg()+
		" or coalesce(p.employee_number,'') ilike "+b.arg()+")", like)
	if got, want := b.whereClause(),
		" where org_id = $1 and (p.full_name ilike $2 or p.email ilike $2 or coalesce(p.employee_number,'') ilike $2)"; got != want {
		t.Errorf("whereClause() = %q, want %q", got, want)
	}
	if got, want := len(b.values()), 2; got != want {
		t.Errorf("arg count = %d, want %d (org + one reused value)", got, want)
	}
}

// TestOrgScopeIsAlwaysFirst guards the tenant filter invariant: the org
// clause must own $1 so no later argument can be shifted onto it.
func TestOrgScopeIsAlwaysFirst(t *testing.T) {
	b := orgScope("org-1")
	if !strings.HasPrefix(b.where[0], "org_id = $1") {
		t.Errorf("first clause = %q, want the org scope", b.where[0])
	}
	if len(b.args) == 0 || b.args[0] != "org-1" {
		t.Errorf("first arg = %v, want the org id", b.args[0])
	}
}

// unformattedAdd finds clauses handed to listBuilder.add() with a printf
// verb. add() appends its input verbatim, so "$%d" reaches Postgres
// literally and the query fails at runtime with a syntax error that no
// compiler catches.
var unformattedAdd = regexp.MustCompile(`\.add\("([^"\n]*%[a-z][^"\n]*)"`)

// rawPrintfSQL finds backtick SQL literals that still carry a printf-style
// placeholder. pgx expects literal $1, $2 references; only Go format
// strings may contain $%d, and those are written in double quotes.
var rawPrintfSQL = regexp.MustCompile("`[^`\n]*\\$%d[^`\n]*`")

func TestNoPrintfPlaceholdersInSQL(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, m := range unformattedAdd.FindAllStringSubmatch(string(src), -1) {
			t.Errorf("%s: add() with unformatted placeholder %q; use b.arg() instead", name, m[1])
		}
		for _, m := range rawPrintfSQL.FindAllString(string(src), -1) {
			t.Errorf("%s: raw SQL with a printf placeholder: %s", name, m)
		}
	}
	if checked == 0 {
		t.Fatal("no store source files were inspected; the guard is not running")
	}
}

// assignmentStatusEnum mirrors the assignment_status type in
// migrations/0001_init.sql. 'completed' is deliberately absent: learning
// completion is the completed_at timestamp and the assessment outcome is
// 'passed'.
var assignmentStatusEnum = map[string]bool{
	"not_started": true, "in_progress": true, "pending_review": true,
	"passed": true, "failed": true, "overdue": true,
}

var statusLiteral = regexp.MustCompile(`'(not_started|in_progress|pending_review|passed|failed|overdue|completed)'`)

// TestAssignmentStatusLiterals checks the values compared against
// assignments.status anywhere in the store. lesson_progress.status is free
// text and legitimately holds 'completed', so those lines are exempt.
func TestAssignmentStatusLiterals(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "lp.status") || strings.Contains(trimmed, "lesson_progress") {
				continue
			}
			// Only lines that compare an assignment status are in scope.
			// SQL keywords here are the status columns of the assignments
			// and attempts tables.
			if !mentionsAssignmentStatus(trimmed) {
				continue
			}
			for _, m := range statusLiteral.FindAllStringSubmatch(trimmed, -1) {
				if !assignmentStatusEnum[m[1]] {
					t.Errorf("%s:%d: %q is not an assignment_status value: %s", name, i+1, m[1], trimmed)
				}
			}
		}
	}
}

func mentionsAssignmentStatus(line string) bool {
	return strings.Contains(line, "a.status") ||
		strings.Contains(line, "a2.status") ||
		strings.Contains(line, "at.status") ||
		strings.Contains(line, "assignments a") ||
		strings.Contains(line, "update assignments")
}

// TestJoinedListQueriesQualifyTheOrgScope guards a real 500: assignments and
// attempts are listed through joins of tables that all carry an org_id
// column, so an unqualified tenant filter is rejected by Postgres as
// ambiguous rather than silently filtering the wrong table.
func TestJoinedListQueriesQualifyTheOrgScope(t *testing.T) {
	if got, want := orgScopeAs("a", "org-1").whereClause(), " where a.org_id = $1"; got != want {
		t.Errorf("orgScopeAs whereClause() = %q, want %q", got, want)
	}
	// The scope must still own $1 so later arguments cannot shift onto it.
	b := orgScopeAs("a", "org-1")
	b.add("a.learner_id = "+b.arg(), "learner-1")
	if got, want := b.whereClause(), " where a.org_id = $1 and a.learner_id = $2"; got != want {
		t.Errorf("whereClause() = %q, want %q", got, want)
	}

	src, err := os.ReadFile("assignments.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"func (db *DB) ListAssignments", "func (db *DB) ListAttempts"} {
		body := funcBody(string(src), fn)
		if !strings.Contains(body, "orgScopeAs(") {
			t.Errorf("%s: the org scope must be qualified with the table alias", fn)
		}
	}
}

// funcBody returns the source of the function starting at marker, up to the
// next top-level declaration.
func funcBody(src, marker string) string {
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	if j := strings.Index(rest[1:], "\nfunc "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

// TestItoaMatchesStrconv keeps the hand-rolled helper aligned with the
// standard library, since arg() is built on it.
func TestItoaMatchesStrconv(t *testing.T) {
	for _, n := range []int{0, 1, 2, 9, 10, 100, 9999} {
		if got, want := itoa(n), strconv.Itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}

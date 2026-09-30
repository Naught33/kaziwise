package store

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
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

// TestAssignmentSelectIsNotDoubleKeyword pins the projection/keyword split.
// assignmentSelect is a column list, so a call site that writes
// "select " + assignmentSelect must not yield "select select ..." — Postgres
// rejects that as a syntax error (42601) and the endpoint 500s.
func TestAssignmentSelectIsNotDoubleKeyword(t *testing.T) {
	composed := "select " + assignmentSelect
	if got := strings.Count(strings.ToLower(composed), "select "); got != 2 {
		// Two are legitimate: the outer keyword and the certificates count
		// subquery. A third means the constant supplied its own.
		t.Errorf("select count = %d, want 2 (outer keyword + count subquery):\n%s", got, composed)
	}
	if strings.Contains(strings.ToLower(composed), "select select") {
		t.Errorf("query begins with a doubled select keyword:\n%s", composed)
	}
	if !strings.Contains(composed, "a.id, a.org_id, a.campaign_id") {
		t.Errorf("projection missing the assignment columns:\n%s", composed)
	}
	// The trailing count subquery is a real select and must survive.
	if !strings.Contains(composed, "certificates_issued") {
		t.Errorf("certificates_issued subquery lost:\n%s", composed)
	}
}

// constStartingWithSelect finds package constants whose SQL fragment opens
// with the select keyword. Those are projections or FROM bodies, and a call
// site that prefixes another "select " produces invalid SQL.
var constStartingWithSelect = regexp.MustCompile("(?m)^const (\\w+) = `\\s*select\\b")

// sqlSelectPrefix finds a call site that concatenates the select keyword in
// front of a named constant.
var sqlSelectPrefix = regexp.MustCompile("`select `\\s*\\+\\s*(\\w+)")

// TestNoDoubledSelectKeyword generalises the check above: no SQL constant that
// already begins with "select" may be used behind another "select ". It is
// the same one-token mistake in a different file, and it fails only at
// runtime, on the first request that reaches the query.
func TestNoDoubledSelectKeyword(t *testing.T) {
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
		text := string(src)
		alreadySelect := map[string]bool{}
		for _, m := range constStartingWithSelect.FindAllStringSubmatch(text, -1) {
			alreadySelect[m[1]] = true
		}
		if len(alreadySelect) == 0 {
			continue
		}
		for _, m := range sqlSelectPrefix.FindAllStringSubmatch(text, -1) {
			if alreadySelect[m[1]] {
				line := 1 + strings.Count(text[:strings.Index(text, m[0])], "\n")
				t.Errorf("%s:%d: %q already begins with select; the call site's "+
					"\"select \" prefix makes it \"select select\"", name, line, m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no store source files were inspected; the guard is not running")
	}
}

// anyWithoutCast finds an any($N) membership test whose parameter carries
// no explicit array type. The cast is what lets the server coerce a text[]
// argument back to uuid[], so it is required even though it is not what
// makes the argument encodable in the first place.
var anyWithoutCast = regexp.MustCompile(`any\(\$\d+\)`)

// migrationPath is the schema the queries are written against.
const migrationPath = "../../migrations/0001_init.sql"

// schemaColumns parses the init migration into table -> column names. It is
// the authority for "does this column exist", which Postgres otherwise only
// answers at runtime, as a 500.
func schemaColumns(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read %s: %v", migrationPath, err)
	}
	tables := map[string]map[string]bool{}
	var table string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if m := regexp.MustCompile(`create table (?:if not exists )?(\w+) \($`).FindStringSubmatch(line); m != nil {
			table = m[1]
			tables[table] = map[string]bool{}
			continue
		}
		if table == "" {
			continue
		}
		if line == ");" {
			table = ""
			continue
		}
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		// Constraint continuations are not columns.
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "constraint") || strings.HasPrefix(low, "primary key") ||
			strings.HasPrefix(low, "unique") || strings.HasPrefix(low, "check") ||
			strings.HasPrefix(low, "foreign key") {
			continue
		}
		col := strings.Fields(line)[0]
		col = strings.TrimSuffix(col, ",")
		tables[table][col] = true
	}
	if len(tables) == 0 {
		t.Fatalf("no tables parsed from %s; the guard is not running", migrationPath)
	}
	return tables
}

var (
	fromAliasRE  = regexp.MustCompile(`(?i)\bfrom\s+(\w+)\s+(\w+)\b`)
	orderByColRE = regexp.MustCompile(`(?i)\border\s+by\s+(.*)$`)
	aliasColRE   = regexp.MustCompile(`\b(\w+)\.(\w+)`)
)

// TestOrderByColumnsExist checks every "order by alias.column" against the
// migration. Ordering the question options by created_at failed at runtime
// with SQLSTATE 42703 because that table has no such column, and nothing in
// the build or the unit tests can see it -- Postgres is the only authority,
// and it is only consulted on a live request.
//
// This is deliberately conservative: it only inspects a SQL fragment when
// the same fragment names both the table and the ordering, so a query built
// by concatenating a projection, a join and a trailing "order by" is skipped
// rather than guessed at. That trades coverage for zero false positives.
func TestOrderByColumnsExist(t *testing.T) {
	tables := schemaColumns(t)
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
		for _, frag := range sqlFragments(string(src)) {
			alias := map[string]string{}
			for _, m := range fromAliasRE.FindAllStringSubmatch(frag, -1) {
				if _, ok := tables[m[1]]; ok {
					alias[m[2]] = m[1]
				}
			}
			ob := orderByColRE.FindStringSubmatch(frag)
			if ob == nil {
				continue
			}
			for _, ref := range aliasColRE.FindAllStringSubmatch(ob[1], -1) {
				table, known := alias[ref[1]]
				if !known {
					continue
				}
				if !tables[table][ref[2]] {
					line := 1 + strings.Count(string(src)[:strings.Index(string(src), ob[0])], "\n")
					t.Errorf("%s:%d: order by %s.%s, but table %s has no column %q",
						name, line, ref[1], ref[2], table, ref[2])
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no store source files were inspected; the guard is not running")
	}
}

// sqlFragments returns each backtick-quoted SQL literal, which keeps the
// analysis inside one query instead of merging aliases across all of them.
func sqlFragments(src string) []string {
	var out []string
	for _, m := range regexp.MustCompile("`([^`]*)`").FindAllStringSubmatch(src, -1) {
		if strings.Contains(m[1], "select") || strings.Contains(m[1], "order by") {
			out = append(out, m[1])
		}
	}
	return out
}

// TestAnyMembershipCastsItsParameter pins every any($N) to an explicit
// ::uuid[] cast.
//
// The cast alone is necessary but not sufficient. The pool runs in pgx's
// "exec" mode, which never Describes, so no parameter gets a server-supplied
// type and pgx encodes as text with an unknown type (OID 0). A bare
// uuid.UUID survives that because pgx has a registered codec for the
// underlying [16]byte; []uuid.UUID does not, because its array element type
// cannot be resolved at OID 0. Passing []string via uuidStrings avoids that,
// and a file that builds an any($N) argument must therefore also use
// uuidStrings -- otherwise it reintroduces the "cannot find encode plan"
// failure at runtime, once enough rows exist to build a slice.
func TestAnyMembershipCastsItsParameter(t *testing.T) {
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
		text := string(src)
		usesAny := anyWithoutCast.MatchString(text)
		if !usesAny {
			continue
		}
		for _, m := range anyWithoutCast.FindAllStringIndex(text, -1) {
			line := 1 + strings.Count(text[:m[0]], "\n")
			t.Errorf("%s:%d: %s has no ::uuid[] cast, so the server cannot "+
				"coerce the text[] argument back to uuid[]",
				name, line, text[m[0]:m[1]])
		}
		if !strings.Contains(text, "uuidStrings(") {
			t.Errorf("%s: builds an any($N) array argument but never calls "+
				"uuidStrings; under exec mode pgx cannot encode a []uuid.UUID "+
				"(parameter type resolves to OID 0)", name)
		}
	}
	if checked == 0 {
		t.Fatal("no store source files were inspected; the guard is not running")
	}
}

// TestEmployeeSearchClauseIsQualified pins the employee list's search clause.
// The employee query joins profiles against a lateral stats subquery, so the
// columns must carry the p alias; a clause that got aliased twice
// ("p.p.full_name") is a missing column and the endpoint fails with a 500.
func TestEmployeeSearchClauseIsQualified(t *testing.T) {
	b := userListWhere(uuid.MustParse("00000000-0000-0000-0000-000000000001"), UserListFilter{Search: "ada"})
	got := b.whereClause()
	want := " where p.org_id = $1 and (p.full_name ilike $2 or p.email ilike $2" +
		" or coalesce(p.employee_number,'') ilike $2)"
	if got != want {
		t.Errorf("whereClause() =\n  %q\nwant\n  %q", got, want)
	}
	if strings.Contains(got, "p.p.") {
		t.Errorf("clause double-qualifies a column: %q", got)
	}
	// The tenant filter still owns $1, and the search reuses $2: a single
	// value, because a second copy would be an argument with no placeholder.
	if len(b.values()) != 2 {
		t.Errorf("args = %v, want the org id and one search term", b.values())
	}
}

// TestEmployeeFiltersStayAliased checks every filter, not just the search:
// a bare column name in this query is ambiguous once the lateral joins are
// in place.
func TestEmployeeFiltersStayAliased(t *testing.T) {
	b := userListWhere(uuid.MustParse("00000000-0000-0000-0000-000000000001"), UserListFilter{
		Department: "Operations", Role: "learner", Status: "active", Unassigned: true,
	})
	got := b.whereClause()
	want := " where p.org_id = $1 and p.department = $2 and p.role = $3" +
		" and p.status = $4 and p.manager_id is null"
	if got != want {
		t.Errorf("whereClause() = %q, want %q", got, want)
	}
}

// TestCreateCourseTreeSuppliesRequiredForeignKeys guards the tree tables,
// where the parent key is not optional in the schema. CreateLesson left
// CourseID unset, so every new lesson failed on lessons_course_id_fkey and
// the builder reported a conflict instead of a bug.
func TestCreateCourseTreeSuppliesRequiredForeignKeys(t *testing.T) {
	src, err := os.ReadFile("courses.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	// The insert names course_id, so the argument list must bind a value
	// for it rather than trusting a struct field the caller may omit.
	for _, want := range []string{"select course_id from modules where id = $1 and org_id = $2"} {
		if !strings.Contains(text, want) {
			t.Errorf("CreateLesson must derive course_id from the module: missing %q", want)
		}
	}
	// A lesson may not be attached to a module from another course.
	if !strings.Contains(text, "module does not belong to this course") {
		t.Error("CreateLesson must reject a module that belongs to a different course")
	}
	// The zero uuid must never reach an insert on a foreign key column.
	if strings.Contains(text, "p.CourseID, p.OrgID") {
		t.Error("CreateLesson still binds the caller-supplied course id; bind the derived one")
	}
}

// TestNullableTextClearsRatherThanSkips pins the difference between a field
// that was not sent and one that was sent empty. A PATCH that cannot express
// "clear this" leaves the old value in place and the edit looks like it
// silently failed.
func TestNullableTextClearsRatherThanSkips(t *testing.T) {
	if got := nullableText(nil); got != nil {
		t.Errorf("nullableText(nil) = %v, want nil (field absent)", got)
	}
	empty := ""
	if got := nullableText(&empty); got != nil {
		t.Errorf("nullableText(\"\") = %v, want nil (field cleared)", got)
	}
	value := "keep me"
	if got := nullableText(&value); got != value {
		t.Errorf("nullableText(%q) = %v, want %q", value, got, value)
	}
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

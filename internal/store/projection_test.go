package store

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The projections this guard inspects, as (bare, joined) pairs.
var (
	lessonColsPair     = []string{lessonCols, lessonColsJoined}
	blockColsPair      = []string{blockCols, blockColsJoined}
	questionColsPair   = []string{questionCols, questionColsJoined}
	certColsPair       = []string{certCols, certColsJoined}
	campaignColsPair   = []string{campaignCols, campaignColsJoined}
	attemptColsPair    = []string{attemptCols, attemptColsJoined}
	assetColsSingleton = []string{assetCols}
	moduleColsPair     = []string{moduleCols}
	profileColsPair    = []string{profileCols}
)

var qualifiedCol = regexp.MustCompile(`(?m)^\s*\w+\.\w+`)

// TestCreateProjectionsAreUnqualified pins the contract that made every
// create endpoint return 500. `insert into ... returning <cols>` has no table
// alias in scope, so a projection qualified as `b.id` fails at runtime with
// "missing FROM-clause entry for table b" (SQLSTATE 42P01). Nothing in the
// build or the unit tests could see it, because the SQL is only parsed by
// Postgres when the endpoint is called.
//
// Each table therefore needs two constants: a bare one for INSERT/UPDATE
// RETURNING and a joined one for SELECT.
func TestCreateProjectionsAreUnqualified(t *testing.T) {
	pairs := map[string][]string{
		"lessonCols":   lessonColsPair,
		"blockCols":    blockColsPair,
		"questionCols": questionColsPair,
		"certCols":     certColsPair,
		"campaignCols": campaignColsPair,
		"attemptCols":  attemptColsPair,
		"assetCols":    assetColsSingleton,
		"moduleCols":   moduleColsPair,
		"profileCols":  profileColsPair,
	}
	for name, variants := range pairs {
		// The first variant is the one used by INSERT ... RETURNING.
		if qualifiedCol.MatchString(variants[0]) {
			t.Errorf("%s is table-qualified, but it is returned from an "+
				"INSERT ... RETURNING where no alias is in scope:\n%s",
				name, variants[0])
		}
	}
}

// TestJoinedProjectionsAreQualified is the other half of the contract: the
// read paths join, so a projection used there must name its alias. Without
// this, "fixing" the create 500 by stripping the alias from a shared
// constant would just move the failure to the list and detail endpoints.
func TestJoinedProjectionsAreQualified(t *testing.T) {
	joined := map[string]string{
		"lessonColsJoined":   lessonColsJoined,
		"blockColsJoined":    blockColsJoined,
		"questionColsJoined": questionColsJoined,
		"certColsJoined":     certColsJoined,
		"campaignColsJoined": campaignColsJoined,
		"attemptColsJoined":  attemptColsJoined,
	}
	for name, projection := range joined {
		if !qualifiedCol.MatchString(projection) {
			t.Errorf("%s is unqualified, but it is selected from a join:\n%s",
				name, projection)
		}
	}
}

// TestProjectionPairsAgree guards against the two constants drifting apart,
// which would silently return columns in the wrong order and mis-scan every
// row. The joined form must be the bare form with an alias added.
func TestProjectionPairsAgree(t *testing.T) {
	pairs := map[string][2]string{
		"lessonCols":   {lessonCols, lessonColsJoined},
		"blockCols":    {blockCols, blockColsJoined},
		"questionCols": {questionCols, questionColsJoined},
		"certCols":     {certCols, certColsJoined},
		"campaignCols": {campaignCols, campaignColsJoined},
		"attemptCols":  {attemptCols, attemptColsJoined},
	}
	columnOf := func(line string) string {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			return ""
		}
		col := strings.TrimSuffix(fields[0], ",")
		if i := strings.Index(col, "."); i >= 0 {
			col = col[i+1:]
		}
		return col
	}
	split := func(projection string) []string {
		var out []string
		for _, line := range strings.Split(projection, "\n") {
			if c := columnOf(line); c != "" {
				out = append(out, c)
			}
		}
		return out
	}
	for name, pair := range pairs {
		bare, joined := split(pair[0]), split(pair[1])
		if len(bare) == 0 {
			t.Fatalf("%s parses as no columns", name)
		}
		if len(bare) == 1 && len(joined) == 1 && bare[0] == joined[0] {
			// A projection that is genuinely a single bare column, such as
			// the primary keys the organisation create path returns.
			continue
		}
		if len(bare) != len(joined) {
			t.Errorf("%s: bare has %d columns, joined has %d",
				name, len(bare), len(joined))
			continue
		}
		for i := range bare {
			if bare[i] != joined[i] {
				t.Errorf("%s: column %d is %q in the bare projection but %q in the joined one",
					name, i, bare[i], joined[i])
			}
		}
	}
}

// countProjectionColumns counts the columns in a projection. A line can hold
// several, so this splits on commas rather than counting lines.
func countProjectionColumns(projection string) int {
	n := 0
	for _, line := range strings.Split(projection, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		for _, part := range strings.Split(line, ",") {
			if strings.TrimSpace(part) != "" {
				n++
			}
		}
	}
	return n
}

// countRow stands in for a pgx row so a scanner's destination count can be
// inspected without a database.
type countRow struct{ n int }

func (r *countRow) Scan(dest ...any) error { r.n = len(dest); return nil }

// TestScanAttemptDestinationsMatchTheSelectedColumns guards the shared
// attempt scanner. Both callers select attemptColsJoined plus c.pass_mark,
// but the scanner used to take the pass mark as a Go argument and never
// scanned it, so pgx saw 13 field descriptions against 12 destinations and
// every attempt read failed at runtime with SQLSTATE 42P08. Like the
// projection guards above, no build or unit test could catch it, because the
// SQL is only parsed by Postgres when the endpoint is called.
func TestScanAttemptDestinationsMatchTheSelectedColumns(t *testing.T) {
	want := countProjectionColumns(attemptColsJoined) + 1 // plus c.pass_mark

	row := &countRow{}
	if _, err := scanAttempt(row); err != nil {
		t.Fatalf("scanAttempt: %v", err)
	}
	if row.n != want {
		t.Errorf("scanAttempt passed %d destinations, but the select has %d "+
			"columns (attemptColsJoined plus c.pass_mark)", row.n, want)
	}
}

// passMarkSlot is where c.pass_mark lands: after every column of
// attemptColsJoined.
const passMarkSlot = 12

// assignRow fills destinations positionally, so the pass mark can be told
// apart from the other numeric columns of the projection.
type assignRow struct{}

func (assignRow) Scan(dest ...any) error {
	for i, d := range dest {
		switch p := d.(type) {
		case *float64:
			if i == passMarkSlot {
				*p = 70
			} else {
				*p = 1
			}
		case *string:
			*p = "in_progress"
		case *uuid.UUID:
			*p = uuid.New()
		}
	}
	return nil
}

// TestScanAttemptReadsThePassMark pins that the pass mark comes from the row
// rather than from a caller-supplied argument. Handing it in instead left
// every listed attempt reporting a pass mark of zero.
func TestScanAttemptReadsThePassMark(t *testing.T) {
	a, err := scanAttempt(assignRow{})
	if err != nil {
		t.Fatalf("scanAttempt: %v", err)
	}
	if a.PassMark != 70 {
		t.Errorf("PassMark = %v, want the 70 the row carried", a.PassMark)
	}
}

package api

import (
	"context"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// reportTemplate renders the training report for printing to PDF or
// opening in a spreadsheet. Every value goes through html/template, so a
// learner's name cannot inject markup into the export.
var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"pct": func(v float64) string { return formatFloat(v) + "%" },
	"date": func(v *time.Time) string {
		if v == nil {
			return "-"
		}
		return v.Format("2006-01-02")
	},
	"score": func(v *float64) string {
		if v == nil {
			return "-"
		}
		return formatFloat(*v) + "%"
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>KaziWise training report</title>
<style>
  body { font-family: "Segoe UI", system-ui, sans-serif; color: #10203a; margin: 32px; }
  h1 { font-size: 20px; margin: 0 0 4px; }
  p.meta { color: #5b6b86; margin: 0 0 20px; font-size: 13px; }
  table { border-collapse: collapse; width: 100%; font-size: 13px; }
  th, td { border: 1px solid #d7dfea; padding: 7px 9px; text-align: left; }
  th { background: #f3f6fb; }
  td.num { text-align: right; }
  tfoot td { font-weight: 600; background: #f9fbfd; }
</style>
</head>
<body>
<h1>KaziWise training report</h1>
<p class="meta">{{ .OrgName }} &middot; generated {{ .GeneratedAt }} &middot; {{ .Total }} learner(s)</p>
<table>
  <thead>
    <tr>
      <th>Learner</th><th>Email</th><th>Department</th><th>Campaign</th><th>Course</th>
      <th>Status</th><th class="num">Progress</th><th class="num">Best score</th><th>Due</th>
    </tr>
  </thead>
  <tbody>
  {{- range .Rows }}
    <tr>
      <td>{{ .LearnerName }}</td>
      <td>{{ .LearnerEmail }}</td>
      <td>{{ .Department }}</td>
      <td>{{ .CampaignName }}</td>
      <td>{{ .CourseTitle }}</td>
      <td>{{ .Status }}</td>
      <td class="num">{{ pct .ProgressPct }}</td>
      <td class="num">{{ score .BestScore }}</td>
      <td>{{ date .DueDate }}</td>
    </tr>
  {{- else }}
    <tr><td colspan="9">No training matches this filter.</td></tr>
  {{- end }}
  </tbody>
  <tfoot>
    <tr><td colspan="9">{{ .Total }} row(s)</td></tr>
  </tfoot>
</table>
</body>
</html>`))

type reportView struct {
	OrgName     string
	GeneratedAt string
	Total       int
	Rows        []domain.Assignment
}

// formatFloat prints a percentage without trailing zeroes, so a report
// reads 62.5% rather than 62.500000%.
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// renderReportHTML builds the printable report. The organisation name is
// read for the same org the rows are scoped to, so an export can never be
// labelled with another tenant's name.
func renderReportHTML(db *store.DB, orgID uuid.UUID, rows []domain.Assignment, total int) (string, error) {
	view := reportView{
		GeneratedAt: time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		Total:       total,
		Rows:        rows,
	}
	// A missing org record must not fail the export; the label is cosmetic.
	if org, err := db.OrgByID(context.Background(), orgID); err == nil && org != nil {
		view.OrgName = org.Name
	} else {
		view.OrgName = "Organisation"
	}
	var sb strings.Builder
	if err := reportTemplate.Execute(&sb, view); err != nil {
		return "", fmt.Errorf("render report: %w", err)
	}
	return sb.String(), nil
}

package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

// ---------------------------------------------------------------------
// Dashboard KPIs (screen 01)
// ---------------------------------------------------------------------

// DashboardKPIs is one row of scalar counts, computed in a single
// statement so the numbers on a screen can never disagree with each other.
func (db *DB) DashboardKPIs(ctx context.Context, orgID uuid.UUID) (*domain.DashboardKPIs, error) {
	var k domain.DashboardKPIs
	var avg *float64
	err := db.pool.QueryRow(ctx, `
		select
		  (select count(*) from profiles p
		     where p.org_id = $1 and p.role in ('learner','manager'))::int,
		  (select count(*) from profiles p
		     where p.org_id = $1 and p.status = 'active'
		       and p.role in ('learner','manager'))::int,
		  (select count(distinct coalesce(department,'')) from profiles p
		     where p.org_id = $1 and department is not null)::int,
		  (select count(*) from courses c where c.org_id = $1)::int,
		  (select count(*) from courses c where c.org_id = $1 and c.status = 'published')::int,
		  (select count(*) from courses c where c.org_id = $1 and c.status = 'draft')::int,
		  (select count(*) from campaigns c where c.org_id = $1 and c.status = 'active')::int,
		  (select count(*) from assignments a where a.org_id = $1)::int,
		  (select count(*) from assignments a where a.org_id = $1
		     and a.status in ('in_progress','pending_review'))::int,
		  (select count(*) from assignments a where a.org_id = $1
		     and a.lessons_done > 0 and a.status <> 'passed')::int,
		  (select count(*) from assignments a where a.org_id = $1 and a.status = 'passed')::int,
		  (select count(*) from assignments a where a.org_id = $1 and a.status = 'failed')::int,
		  (select count(*) from assignments a where a.org_id = $1
		     and a.due_date is not null and a.due_date < current_date
		     and a.status in ('not_started','in_progress','pending_review'))::int,
		  (select count(*) from attempts at where at.org_id = $1
		     and at.status = 'pending_review')::int,
		  (select count(*) from certificates ct where ct.org_id = $1
		     and ct.revoked_at is null)::int,
		  (select round(avg(a.final_score), 2) from assignments a
		     where a.org_id = $1 and a.final_score is not null)`,
		orgID).Scan(&k.Employees, &k.ActiveEmployees, &k.Departments, &k.TotalCourses,
		&k.PublishedCourses, &k.DraftCourses, &k.ActiveCampaigns, &k.Assigned, &k.Started,
		&k.Completed, &k.Passed, &k.Failed, &k.Overdue, &k.PendingReview,
		&k.CertificatesIssued, &avg)
	if err != nil {
		return nil, mapErr(err)
	}
	k.AvgScore = avg
	if k.Assigned > 0 {
		k.CompletionPct = round2(float64(k.Passed) / float64(k.Assigned) * 100)
	}
	// Pass rate is measured against learners who have sat the assessment,
	// not against everyone assigned, otherwise a single pass reads as 3%.
	graded := k.Passed + k.Failed
	if graded > 0 {
		k.PassRatePct = round2(float64(k.Passed) / float64(graded) * 100)
	}
	return &k, nil
}

// AttentionItems powers the "Attention Required" list.
func (db *DB) AttentionItems(ctx context.Context, orgID uuid.UUID, limit int) ([]domain.AttentionItem, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.pool.Query(ctx, `
		(select 'overdue'::text as type, 'danger'::text as severity,
		        a.learner_id, p.full_name, co.title,
		        'Overdue since ' || to_char(a.due_date, 'DD Mon YYYY') as detail,
		        a.due_date,
		        '/v1/assignments/' || a.id as url,
		        a.last_activity_at as sort
		 from assignments a
		 join profiles p on p.id = a.learner_id
		 join courses co on co.id = a.course_id
		 where a.org_id = $1 and a.due_date is not null
		   and a.due_date < current_date
		   and a.status in ('not_started','in_progress','pending_review'))
		union all
		(select 'failed', 'warning', a.learner_id, p.full_name, co.title,
		        'Failed at ' || coalesce(a.final_score, 0) || '% after ' ||
		          a.attempts_used || ' attempt(s)',
		        a.due_date, '/v1/assignments/' || a.id, a.last_activity_at
		 from assignments a
		 join profiles p on p.id = a.learner_id
		 join courses co on co.id = a.course_id
		 where a.org_id = $1 and a.status = 'failed')
		union all
		(select 'pending_review', 'info', at.learner_id, p.full_name, co.title,
		        'Assessment awaiting manual grading',
		        a.due_date, '/v1/attempts/' || at.id, at.submitted_at
		 from attempts at
		 join assignments a on a.id = at.assignment_id
		 join profiles p on p.id = at.learner_id
		 join courses co on co.id = a.course_id
		 where at.org_id = $1 and at.status = 'pending_review')
		union all
		(select 'due_soon', 'warning', a.learner_id, p.full_name, co.title,
		        'Due in ' || (a.due_date - current_date) || ' day(s)',
		        a.due_date, '/v1/assignments/' || a.id, a.last_activity_at
		 from assignments a
		 join profiles p on p.id = a.learner_id
		 join courses co on co.id = a.course_id
		 where a.org_id = $1 and a.due_date is not null
		   and a.due_date >= current_date and a.due_date <= current_date + 7
		   and a.status in ('not_started','in_progress','pending_review'))
		order by sort desc nulls last
		limit $2`, orgID, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []domain.AttentionItem{}
	for rows.Next() {
		var it domain.AttentionItem
		if err := rows.Scan(&it.Type, &it.Severity, &it.LearnerID, &it.LearnerName,
			&it.CourseTitle, &it.Detail, &it.DueDate, &it.URL); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, it)
	}
	return out, mapErr(rows.Err())
}

// DepartmentProgress is the per-department rollup for reports.
func (db *DB) DepartmentProgress(ctx context.Context, orgID uuid.UUID, campaignID *uuid.UUID) ([]domain.DepartmentProgress, error) {
	rows, err := db.pool.Query(ctx, `
		select coalesce(p.department, 'Unassigned') as dept,
		       count(distinct p.id)::int as headcount,
		       count(a.id)::int as assigned,
		       count(a.id) filter (where a.lessons_done > 0)::int as completed,
		       count(a.id) filter (where a.status = 'passed')::int as passed,
		       count(a.id) filter (where a.due_date is not null
		           and a.due_date < current_date
		           and a.status in ('not_started','in_progress','pending_review'))::int as overdue
		from profiles p
		left join assignments a on a.learner_id = p.id
		     and ($2::uuid is null or a.campaign_id = $2)
		where p.org_id = $1 and p.role in ('learner','manager')
		group by coalesce(p.department, 'Unassigned')
		order by dept`, orgID, campaignID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []domain.DepartmentProgress{}
	for rows.Next() {
		var d domain.DepartmentProgress
		if err := rows.Scan(&d.Department, &d.Headcount, &d.Assigned, &d.Completed,
			&d.Passed, &d.Overdue); err != nil {
			return nil, mapErr(err)
		}
		if d.Assigned > 0 {
			d.CompletionPct = round2(float64(d.Completed) / float64(d.Assigned) * 100)
		}
		if d.Passed > 0 {
			d.PassRatePct = round2(float64(d.Passed) / float64(d.Assigned) * 100)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

// StatusBreakdown counts the tracking states across a filtered set.
func (db *DB) StatusBreakdown(ctx context.Context, orgID uuid.UUID, f AssignmentFilter) (*domain.StatusBreakdown, error) {
	list, total, err := db.ListAssignments(ctx, orgID, AssignmentFilter{
		LearnerID: f.LearnerID, CampaignID: f.CampaignID, CourseID: f.CourseID,
		ManagerID: f.ManagerID, Department: f.Department, Status: f.Status,
		Overdue: f.Overdue, Search: f.Search, From: f.From, To: f.To,
		Page: 1, PerPage: 100000,
	})
	if err != nil {
		return nil, err
	}
	b := &domain.StatusBreakdown{Total: total}
	var scoreSum float64
	var scoreCount int
	for _, a := range list {
		switch a.Status {
		case domain.AssignNotStarted:
			b.NotStarted++
		case domain.AssignInProgress:
			b.InProgress++
		case domain.AssignPendingReview:
			b.PendingReview++
		case domain.AssignPassed:
			b.Passed++
		case domain.AssignFailed:
			b.Failed++
		case domain.AssignOverdue:
			b.Overdue++
		}
		if a.IsOverdue && a.Status != domain.AssignOverdue {
			b.Overdue++
		}
		if a.FinalScore != nil {
			scoreSum += *a.FinalScore
			scoreCount++
		}
	}
	graded := b.Passed + b.Failed
	if graded > 0 {
		b.PassRatePct = round2(float64(b.Passed) / float64(graded) * 100)
	}
	if scoreCount > 0 {
		b.AvgScorePct = round2(scoreSum / float64(scoreCount))
	}
	return b, nil
}

// ---------------------------------------------------------------------
// Learner dashboard (screen 08)
// ---------------------------------------------------------------------

func (db *DB) LearnerSummary(ctx context.Context, orgID, learnerID uuid.UUID) (assigned, inProgress, completed, passed, failed, overdue int, progress float64, err error) {
	err = db.pool.QueryRow(ctx, `
		select count(*)::int,
		       count(*) filter (where status in ('in_progress','pending_review','overdue'))::int,
		       count(*) filter (where lessons_done > 0 and status <> 'passed')::int,
		       count(*) filter (where status = 'passed')::int,
		       count(*) filter (where status = 'failed')::int,
		       count(*) filter (where due_date is not null and due_date < current_date
		                         and status in ('not_started','in_progress','pending_review'))::int,
		       coalesce(round(avg(progress_percent), 2), 0)
		from assignments
		where org_id = $1 and learner_id = $2`, orgID, learnerID).
		Scan(&assigned, &inProgress, &completed, &passed, &failed, &overdue, &progress)
	return
}

// ---------------------------------------------------------------------
// Export helpers
// ---------------------------------------------------------------------

// ReportCSV renders the report rows as CSV. Kept in the store layer so the
// exact same filter set backs the screen and the export.
func (db *DB) ReportCSV(ctx context.Context, orgID uuid.UUID, f AssignmentFilter) (string, error) {
	rows, _, err := db.ListAssignments(ctx, orgID, AssignmentFilter{
		LearnerID: f.LearnerID, CampaignID: f.CampaignID, CourseID: f.CourseID,
		ManagerID: f.ManagerID, Department: f.Department, Status: f.Status,
		Overdue: f.Overdue, Search: f.Search, From: f.From, To: f.To,
		Page: 1, PerPage: 100000, Sort: f.Sort, Order: f.Order,
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("Employee,Email,Department,Manager,Campaign,Course,Status," +
		"Progress %,Best Score,Final Score,Attempts,Assigned At,Due Date,Last Activity\n")
	for _, a := range rows {
		fields := []string{
			a.LearnerName, a.LearnerEmail, deref(a.Department), deref(a.ManagerName),
			a.CampaignName, a.CourseTitle, string(a.Status),
			trimNum(a.ProgressPct), trimNum(derefF(a.BestScore)), trimNum(derefF(a.FinalScore)),
			itoa(a.AttemptsUsed), a.AssignedAt.Format("2006-01-02 15:04"),
			dateStr(a.DueDate), a.LastActivityAt.Format("2006-01-02 15:04"),
		}
		sb.WriteString(csvLine(fields))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefF(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// trimNum renders a float without a trailing ".00" so the CSV reads well
// in a spreadsheet.
func trimNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func dateStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// csvLine quotes any field containing a comma, quote or newline.
func csvLine(fields []string) string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.ContainsAny(f, `",`+"\n\r") {
			f = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		}
		out = append(out, f)
	}
	return strings.Join(out, ",")
}

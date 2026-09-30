package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

const assignmentCols = `
	a.id, a.org_id, a.campaign_id, a.course_id, a.learner_id, a.status, a.assigned_at,
	a.due_date, a.started_at, a.last_activity_at, a.completed_at, a.lessons_total,
	a.lessons_done, a.progress_percent, a.attempts_used, a.best_score, a.final_score,
	a.passed_at, a.overdue_notified_at`

const assignmentJoins = `
	join campaigns c on c.id = a.campaign_id
	join courses  co on co.id = a.course_id
	join profiles p  on p.id = a.learner_id
	left join profiles m on m.id = p.manager_id`

const assignmentSelect = `
	select ` + assignmentCols + `,
	       c.name, co.title, p.full_name, p.email, p.department, p.manager_id, m.full_name,
	       (a.due_date is not null and a.status in ('not_started','in_progress','pending_review')
	        and a.due_date < current_date) as is_overdue,
	       (select count(*) from certificates cert where cert.learner_id = a.learner_id
	          and cert.course_id = a.course_id)::int as certificates_issued`

func scanAssignment(row interface{ Scan(...any) error }) (*domain.Assignment, error) {
	var a domain.Assignment
	err := row.Scan(&a.ID, &a.OrgID, &a.CampaignID, &a.CourseID, &a.LearnerID, &a.Status,
		&a.AssignedAt, &a.DueDate, &a.StartedAt, &a.LastActivityAt, &a.CompletedAt,
		&a.LessonsTotal, &a.LessonsDone, &a.ProgressPct, &a.AttemptsUsed, &a.BestScore,
		&a.FinalScore, &a.PassedAt, &a.OverdueSentAt,
		&a.CampaignName, &a.CourseTitle, &a.LearnerName, &a.LearnerEmail, &a.Department,
		&a.ManagerID, &a.ManagerName, &a.IsOverdue, &a.Certificates)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

func (db *DB) AssignmentByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Assignment, error) {
	return scanAssignment(db.pool.QueryRow(ctx,
		`select `+assignmentSelect+` from assignments a`+assignmentJoins+`
		 where a.org_id = $1 and a.id = $2`, orgID, id))
}

// AssignmentForLearner is the tenant-safe way for a learner to reach one
// of their own rows.
func (db *DB) AssignmentForLearner(ctx context.Context, orgID, learnerID, id uuid.UUID) (*domain.Assignment, error) {
	return scanAssignment(db.pool.QueryRow(ctx,
		`select `+assignmentSelect+` from assignments a`+assignmentJoins+`
		 where a.org_id = $1 and a.id = $2 and a.learner_id = $3`, orgID, id, learnerID))
}

type AssignmentFilter struct {
	LearnerID  *uuid.UUID
	CampaignID *uuid.UUID
	CourseID   *uuid.UUID
	ManagerID  *uuid.UUID
	Department string
	Status     string
	Overdue    bool
	Search     string
	From       *string
	To         *string
	Page       int
	PerPage    int
	Sort       string
	Order      string
}

func (db *DB) ListAssignments(ctx context.Context, orgID uuid.UUID, f AssignmentFilter) ([]domain.Assignment, int, error) {
	// Qualified: the org scope shares this query with four other org-scoped
	// tables, and an unqualified org_id is ambiguous there.
	b := orgScopeAs("a", orgID)
	if f.LearnerID != nil {
		b.add("a.learner_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.LearnerID)
	}
	if f.CampaignID != nil {
		b.add("a.campaign_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.CampaignID)
	}
	if f.CourseID != nil {
		b.add("a.course_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.CourseID)
	}
	// A manager only ever sees their own direct reports.
	if f.ManagerID != nil {
		b.add("p.manager_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.ManagerID)
	}
	if f.Department != "" {
		b.add("p.department = $" + itoa(len(b.values())+1))
		b.args = append(b.args, f.Department)
	}
	if f.Status != "" {
		// "overdue" is derived, not a stored status.
		if f.Status == "overdue" {
			b.add("a.due_date is not null and a.due_date < current_date and a.status not in ('passed','failed')")
		} else {
			b.add("a.status = $" + itoa(len(b.values())+1))
			b.args = append(b.args, f.Status)
		}
	}
	if f.Overdue {
		b.add("a.due_date is not null and a.due_date < current_date and a.status not in ('passed','failed')")
	}
	if f.Search != "" {
		i := len(b.values()) + 1
		b.add("(p.full_name ilike $" + itoa(i) + " or p.email ilike $" + itoa(i) + " or co.title ilike $" + itoa(i) + ")")
		b.args = append(b.args, "%"+f.Search+"%")
	}
	if f.From != nil {
		b.add("a.assigned_at >= $" + itoa(len(b.values())+1) + "::timestamptz")
		b.args = append(b.args, *f.From)
	}
	if f.To != nil {
		b.add("a.assigned_at <= $" + itoa(len(b.values())+1) + "::timestamptz")
		b.args = append(b.args, *f.To)
	}

	base := `select ` + assignmentSelect + ` from assignments a` + assignmentJoins + b.whereClause()
	countSQL := `select count(*) from assignments a` + assignmentJoins + b.whereClause()

	sortCol := map[string]string{
		"": "a.last_activity_at", "name": "p.full_name", "status": "a.status",
		"due": "a.due_date", "score": "a.final_score", "progress": "a.progress_percent",
		"assigned": "a.assigned_at", "activity": "a.last_activity_at",
	}[f.Sort]
	if sortCol == "" {
		sortCol = "a.last_activity_at"
	}
	order := "desc"
	if strings.EqualFold(f.Order, "asc") {
		order = "asc"
	}
	base += " order by " + sortCol + " " + order + ", a.id asc"

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Assignment{}
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, mapErr(rows.Err())
}

// ---------------------------------------------------------------------
// Lesson + block progress
// ---------------------------------------------------------------------

// StartLesson records the first touch of a lesson, which is what flips an
// assignment from not_started to in_progress.
func (db *DB) StartLesson(ctx context.Context, orgID, lessonID, learnerID uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		insert into lesson_progress (lesson_id, learner_id, org_id, status)
		values ($1,$2,$3,'in_progress')
		on conflict (lesson_id, learner_id) do nothing`, lessonID, learnerID, orgID)
	return mapErr(err)
}

// CompleteLesson marks a lesson done and recomputes the assignment's
// rollup counters in the same transaction.
func (db *DB) CompleteLesson(ctx context.Context, orgID, lessonID, learnerID uuid.UUID) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `
			insert into lesson_progress (lesson_id, learner_id, org_id, status, completed_at)
			values ($1,$2,$3,'completed', now())
			on conflict (lesson_id, learner_id) do update
			  set status = 'completed',
			      completed_at = coalesce(lesson_progress.completed_at, now())`,
			lessonID, learnerID, orgID); err != nil {
			return mapErr(err)
		}
		return refreshAssignmentProgress(ctx, tx, orgID, nil, learnerID)
	})
}

// LessonStatusMap reports each lesson's status for one learner.
func (db *DB) LessonStatusMap(ctx context.Context, orgID, learnerID, courseID uuid.UUID) (map[uuid.UUID]string, map[uuid.UUID]*string, error) {
	rows, err := db.pool.Query(ctx, `
		select l.id, coalesce(lp.status, ''), lp.completed_at
		from lessons l
		left join lesson_progress lp on lp.lesson_id = l.id and lp.learner_id = $2
		where l.org_id = $1 and l.course_id = $3`, orgID, learnerID, courseID)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	defer rows.Close()
	status := map[uuid.UUID]string{}
	completedAt := map[uuid.UUID]*string{}
	for rows.Next() {
		var id uuid.UUID
		var st string
		var ca *string
		if err := rows.Scan(&id, &st, &ca); err != nil {
			return nil, nil, mapErr(err)
		}
		status[id] = st
		completedAt[id] = ca
	}
	return status, completedAt, mapErr(rows.Err())
}

// refreshAssignmentProgress recomputes lessons_done, lessons_total,
// progress_percent, status and completed_at from the lesson ledger. It is
// called after every lesson action so the learner dashboard, the reports
// and the campaign counters can never drift from lesson_progress.
//
// Scoped either to one assignment (assignmentID set) or to every
// assignment of one learner. A passed or failed assignment is never
// downgraded: the assessment outcome outranks the learning progress.
func refreshAssignmentProgress(ctx context.Context, tx pgxTx, orgID uuid.UUID, assignmentID *uuid.UUID, learnerID uuid.UUID) error {
	filter := "a2.learner_id = $2"
	args := []any{orgID, learnerID}
	if assignmentID != nil {
		filter = "a2.id = $2"
		args = []any{orgID, *assignmentID}
	}

	// Lessons that carry at least one content block form the learning
	// requirement; an assessment-only lesson does not gate progress.
	rollupSQL := `
		update assignments a set
			lessons_total = agg.total,
			lessons_done  = agg.done,
			progress_percent = case when agg.total = 0 then 0
				else round((agg.done::numeric / agg.total) * 100, 2) end,
			status = case
				when a.status in ('passed','failed') then a.status
				when agg.total = 0 then a.status
				when agg.done > 0 or a.started_at is not null then 'in_progress'
				else a.status end,
			completed_at = case
				when agg.total > 0 and agg.done >= agg.total then coalesce(a.completed_at, now())
				else a.completed_at end,
			last_activity_at = now(),
			updated_at = now()
		from (
			select a2.id,
			       (select count(*) from lessons l
			         where l.course_id = a2.course_id
			           and exists (select 1 from blocks b where b.lesson_id = l.id))::int as total,
			       (select count(*) from lessons l
			         where l.course_id = a2.course_id
			           and exists (select 1 from blocks b where b.lesson_id = l.id)
			           and exists (select 1 from lesson_progress lp
			                        where lp.lesson_id = l.id
			                          and lp.learner_id = a2.learner_id
			                          and lp.status = 'completed'))::int as done
			from assignments a2
			where ` + filter + `
		) agg
		where a.id = agg.id`

	if _, err := tx.Exec(ctx, rollupSQL, args...); err != nil {
		return mapErr(err)
	}

	// Overdue is derived from the deadline, so it is recomputed on every
	// touch rather than only by a scheduled job.
	overdueSQL := `
		update assignments set status = 'overdue', updated_at = now()
		where org_id = $1 and ` + filter + `
		  and due_date is not null and due_date < current_date
		  and status in ('not_started','in_progress','pending_review')`
	if _, err := tx.Exec(ctx, overdueSQL, args...); err != nil {
		return mapErr(err)
	}
	return nil
}

// RecomputeAllProgress is used after a campaign launches so newly created
// assignment rows pick up the correct lesson totals.
func (db *DB) RecomputeAllProgress(ctx context.Context, orgID, campaignID uuid.UUID) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `
			update assignments a set lessons_total = (
				select count(*) from lessons l where l.course_id = a.course_id
				  and exists (select 1 from blocks b where b.lesson_id = l.id)
			)
			where a.org_id = $1 and a.campaign_id = $2`, orgID, campaignID); err != nil {
			return mapErr(err)
		}
		_, err := tx.Exec(ctx, `
			update assignments a set progress_percent = case
				when a.lessons_total = 0 then 0
				else round((a.lessons_done::numeric / a.lessons_total) * 100, 2) end
			where a.org_id = $1 and a.campaign_id = $2`, orgID, campaignID)
		return mapErr(err)
	})
}

// MarkOverdue runs on a schedule or on demand from the admin dashboard.
func (db *DB) MarkOverdue(ctx context.Context, orgID uuid.UUID) (int, error) {
	tag, err := db.pool.Exec(ctx, `
		update assignments set status = 'overdue', updated_at = now()
		where org_id = $1 and due_date is not null and due_date < current_date
		  and status in ('not_started','in_progress','pending_review')`, orgID)
	if err != nil {
		return 0, mapErr(err)
	}
	return int(tag.RowsAffected()), nil
}

// TouchAssignment updates last_activity_at, used by every learner action.
func (db *DB) TouchAssignment(ctx context.Context, assignmentID uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		update assignments
		set last_activity_at = now(),
		    started_at = coalesce(started_at, now()),
		    status = case when status = 'not_started' then 'in_progress' else status end,
		    updated_at = now()
		where id = $1 and status not in ('passed','failed')`, assignmentID)
	return mapErr(err)
}

// ---------------------------------------------------------------------
// Attempts
// ---------------------------------------------------------------------

const attemptCols = `
	at.id, at.assignment_id, at.learner_id, at.attempt_number, at.status, at.max_score,
	at.score, at.percent, at.passed, at.started_at, at.submitted_at, at.graded_at`

func scanAttempt(row interface{ Scan(...any) error }, passMark float64) (*domain.Attempt, error) {
	var a domain.Attempt
	err := row.Scan(&a.ID, &a.AssignmentID, &a.LearnerID, &a.AttemptNumber, &a.Status,
		&a.MaxScore, &a.Score, &a.Percent, &a.Passed, &a.StartedAt, &a.SubmittedAt, &a.GradedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	a.PassMark = passMark
	return &a, nil
}

// AssignmentContext carries the rules a learner attempt is judged by.
// AssignmentContext carries the rules a learner attempt is judged by,
// resolved in one query so a submission never reads stale rules.
type AssignmentContext struct {
	AssignmentID     uuid.UUID
	OrgID            uuid.UUID
	CourseID         uuid.UUID
	CourseTitle      string
	LearnerID        uuid.UUID
	LearnerName      string
	PassMark         float64
	MaxAttempts      int
	AttemptsUsed     int
	RequireLearning  bool
	IssueCertificate bool
	LessonsTotal     int
	LessonsDone      int
	LearningComplete bool
	CampaignID       uuid.UUID
	CampaignStatus   string
	Status           domain.AssignmentStatus
	DueDate          *time.Time
	TotalPoints      float64
}

func (db *DB) AssignmentContext(ctx context.Context, orgID, assignmentID uuid.UUID) (*AssignmentContext, error) {
	var ac AssignmentContext
	err := db.pool.QueryRow(ctx, `
		select a.id, a.org_id, a.course_id, co.title, a.learner_id, p.full_name,
		       c.pass_mark, c.max_attempts, a.attempts_used, c.require_learning,
		       c.issue_certificate, a.lessons_total, a.lessons_done, c.id,
		       c.status::text, a.status::text, a.due_date,
		       coalesce((select sum(q.points) from questions q where q.course_id = a.course_id), 0)
		from assignments a
		join campaigns c on c.id = a.campaign_id
		join courses co on co.id = a.course_id
		join profiles p on p.id = a.learner_id
		where a.org_id = $1 and a.id = $2`, orgID, assignmentID).
		Scan(&ac.AssignmentID, &ac.OrgID, &ac.CourseID, &ac.CourseTitle, &ac.LearnerID,
			&ac.LearnerName, &ac.PassMark, &ac.MaxAttempts, &ac.AttemptsUsed,
			&ac.RequireLearning, &ac.IssueCertificate, &ac.LessonsTotal, &ac.LessonsDone,
			&ac.CampaignID, &ac.CampaignStatus, &ac.Status, &ac.DueDate, &ac.TotalPoints)
	if err != nil {
		return nil, mapErr(err)
	}
	ac.LearningComplete = ac.LessonsTotal == 0 || ac.LessonsDone >= ac.LessonsTotal
	return &ac, nil
}

func (db *DB) CreateAttempt(ctx context.Context, orgID, assignmentID, learnerID uuid.UUID, maxScore float64) (*domain.Attempt, error) {
	var a domain.Attempt
	err := db.pool.QueryRow(ctx, `
		insert into attempts (org_id, assignment_id, learner_id, attempt_number, max_score)
		values ($1,$2,$3,
		        coalesce((select max(attempt_number)+1 from attempts where assignment_id = $2), 1),
		        $4)
		returning `+attemptCols,
		orgID, assignmentID, learnerID, maxScore).
		Scan(&a.ID, &a.AssignmentID, &a.LearnerID, &a.AttemptNumber, &a.Status,
			&a.MaxScore, &a.Score, &a.Percent, &a.Passed, &a.StartedAt, &a.SubmittedAt, &a.GradedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	_, err = db.pool.Exec(ctx,
		`update assignments set attempts_used = attempts_used + 1, updated_at = now()
		 where id = $1`, assignmentID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

func (db *DB) AttemptByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Attempt, error) {
	return scanAttempt(db.pool.QueryRow(ctx, `
		select `+attemptCols+`, c.pass_mark
		from attempts at
		join assignments a2 on a2.id = at.assignment_id
		join campaigns c on c.id = a2.campaign_id
		where at.org_id = $1 and at.id = $2`, orgID, id), 0)
}

// AttemptDetail returns an attempt with its answers, the pass mark and the
// per-answer question context a grading screen needs.
func (db *DB) AttemptDetail(ctx context.Context, orgID, id uuid.UUID) (*domain.Attempt, error) {
	var a domain.Attempt
	var passMark float64
	var gradedByName *string
	err := db.pool.QueryRow(ctx, `
		select `+attemptCols+`, c.pass_mark, g.full_name
		from attempts at
		join assignments a2 on a2.id = at.assignment_id
		join campaigns c on c.id = a2.campaign_id
		left join profiles g on g.id = at.graded_by
		where at.org_id = $1 and at.id = $2`, orgID, id).
		Scan(&a.ID, &a.AssignmentID, &a.LearnerID, &a.AttemptNumber, &a.Status,
			&a.MaxScore, &a.Score, &a.Percent, &a.Passed, &a.StartedAt, &a.SubmittedAt,
			&a.GradedAt, &passMark, &gradedByName)
	if err != nil {
		return nil, mapErr(err)
	}
	a.PassMark = passMark
	a.GradedByName = gradedByName

	answers, pending, err := db.AnswersForAttempt(ctx, orgID, a.ID)
	if err != nil {
		return nil, err
	}
	a.Answers = answers
	a.PendingManual = pending
	a.ReviewNeeded = pending > 0
	return &a, nil
}

// AnswersForAttempt loads every saved response for an attempt, together
// with the count of written answers still awaiting a human grade. It is
// exported because the service layer both grades and rehydrates attempts.
func (db *DB) AnswersForAttempt(ctx context.Context, orgID, attemptID uuid.UUID) ([]domain.Answer, int, error) {
	rows, err := db.pool.Query(ctx, `
		select aa.id, aa.attempt_id, aa.question_id, aa.response_text, aa.selected_option_ids,
		       aa.is_correct, aa.points_awarded, aa.auto_graded, aa.feedback, g.full_name, aa.graded_at
		from attempt_answers aa
		join questions q on q.id = aa.question_id
		left join profiles g on g.id = aa.graded_by
		where aa.org_id = $1 and aa.attempt_id = $2
		order by q.position, aa.id`, orgID, attemptID)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []domain.Answer{}
	pending := 0
	for rows.Next() {
		var ans domain.Answer
		if err := rows.Scan(&ans.ID, &ans.AttemptID, &ans.QuestionID, &ans.ResponseText,
			&ans.SelectedOptionIDs, &ans.IsCorrect, &ans.PointsAwarded, &ans.AutoGraded,
			&ans.Feedback, &ans.GradedByName, &ans.GradedAt); err != nil {
			return nil, 0, mapErr(err)
		}
		if !ans.AutoGraded && ans.PointsAwarded == nil {
			pending++
		}
		out = append(out, ans)
	}
	return out, pending, mapErr(rows.Err())
}

type AttemptFilter struct {
	AssignmentID *uuid.UUID
	LearnerID    *uuid.UUID
	Learners     []uuid.UUID
	Status       string
	NeedsReview  bool
	Page         int
	PerPage      int
}

func (db *DB) ListAttempts(ctx context.Context, orgID uuid.UUID, f AttemptFilter) ([]domain.Attempt, int, error) {
	// Qualified: attempts, assignments and campaigns all carry org_id.
	b := orgScopeAs("at", orgID)
	if f.AssignmentID != nil {
		b.add("at.assignment_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.AssignmentID)
	}
	if f.LearnerID != nil {
		b.add("at.learner_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.LearnerID)
	}
	if len(f.Learners) > 0 {
		b.add("at.learner_id = any($" + itoa(len(b.values())+1) + ")")
		b.args = append(b.args, f.Learners)
	}
	if f.Status != "" {
		b.add("at.status = $" + itoa(len(b.values())+1))
		b.args = append(b.args, f.Status)
	}
	if f.NeedsReview {
		b.add("at.status = 'pending_review'")
	}
	base := `select ` + attemptCols + `, c.pass_mark
		from attempts at
		join assignments a2 on a2.id = at.assignment_id
		join campaigns c on c.id = a2.campaign_id` + b.whereClause() +
		` order by at.started_at desc`
	countSQL := `select count(*) from attempts at
		join assignments a2 on a2.id = at.assignment_id` + b.whereClause()

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Attempt{}
	for rows.Next() {
		a, err := scanAttempt(rows, 0)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, mapErr(rows.Err())
}

// ---------------------------------------------------------------------
// Answers
// ---------------------------------------------------------------------

type UpsertAnswerParams struct {
	OrgID             uuid.UUID
	AttemptID         uuid.UUID
	QuestionID        uuid.UUID
	ResponseText      *string
	SelectedOptionIDs []uuid.UUID
}

// SaveAnswer records or replaces one response. Only choice questions
// persist an option set; text questions persist the typed text.
func (db *DB) SaveAnswer(ctx context.Context, p UpsertAnswerParams) error {
	opts := p.SelectedOptionIDs
	if opts == nil {
		opts = []uuid.UUID{}
	}
	_, err := db.pool.Exec(ctx, `
		insert into attempt_answers (attempt_id, question_id, org_id, response_text, selected_option_ids)
		values ($1,$2,$3,$4,$5)
		on conflict (attempt_id, question_id) do update
		  set response_text = excluded.response_text,
		      selected_option_ids = excluded.selected_option_ids,
		      is_correct = null, points_awarded = null, feedback = null,
		      graded_by = null, graded_at = null, updated_at = now()`,
		p.AttemptID, p.QuestionID, p.OrgID, p.ResponseText, opts)
	return mapErr(err)
}

type AutoGradeResult struct {
	AnswerID      uuid.UUID
	QuestionID    uuid.UUID
	IsCorrect     bool
	PointsAwarded float64
}

// FinalizeAutoGraded writes the outcome of auto-graded choice questions
// for one attempt. Scoping by attempt_id is essential: a learner may have
// several attempts at the same question.
func (db *DB) FinalizeAutoGraded(ctx context.Context, orgID, attemptID uuid.UUID, results []AutoGradeResult) error {
	if len(results) == 0 {
		return nil
	}
	return db.inTx(ctx, func(tx pgxTx) error {
		b := &pgxBatch{}
		for _, r := range results {
			b.add(`update attempt_answers
			       set is_correct = $1, points_awarded = $2, auto_graded = true,
			           graded_at = now(), updated_at = now()
			       where attempt_id = $3 and question_id = $4`,
				r.IsCorrect, r.PointsAwarded, attemptID, r.QuestionID)
		}
		return sendAll(ctx, tx, b)
	})
}

// AnswersNeedingManual lists the text answers still awaiting a grade.
func (db *DB) AnswersNeedingManual(ctx context.Context, orgID, attemptID uuid.UUID) ([]domain.Answer, error) {
	rows, err := db.pool.Query(ctx, `
		select aa.id, aa.attempt_id, aa.question_id, aa.response_text, aa.selected_option_ids,
		       aa.is_correct, aa.points_awarded, aa.auto_graded, aa.feedback, aa.graded_at
		from attempt_answers aa
		join questions q on q.id = aa.question_id
		where aa.org_id = $1 and aa.attempt_id = $2 and q.type in ('short_text','long_text')
		  and aa.points_awarded is null
		order by q.position`, orgID, attemptID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Answer{}
	for rows.Next() {
		var a domain.Answer
		if err := rows.Scan(&a.ID, &a.AttemptID, &a.QuestionID, &a.ResponseText,
			&a.SelectedOptionIDs, &a.IsCorrect, &a.PointsAwarded, &a.AutoGraded,
			&a.Feedback, &a.GradedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

type GradeAnswerParams struct {
	OrgID     uuid.UUID
	AnswerID  uuid.UUID
	GradedBy  uuid.UUID
	Points    float64
	Feedback  *string
	IsCorrect *bool
}

// GradeAnswer records a manual grade. The points ceiling is enforced in
// the service layer against the question's own point value.
func (db *DB) GradeAnswer(ctx context.Context, p GradeAnswerParams) error {
	_, err := db.pool.Exec(ctx, `
		update attempt_answers aa
		set points_awarded = $1, feedback = $2, is_correct = coalesce($3, is_correct),
		    auto_graded = false, graded_by = $4, graded_at = now(), updated_at = now()
		from questions q
		where aa.id = $5 and aa.org_id = $6 and q.id = aa.question_id`,
		p.Points, p.Feedback, p.IsCorrect, p.GradedBy, p.AnswerID, p.OrgID)
	return mapErr(err)
}

// AnswerByID fetches a single answer with its question, for grading.
func (db *DB) AnswerByID(ctx context.Context, orgID, answerID uuid.UUID) (*domain.Answer, error) {
	var a domain.Answer
	err := db.pool.QueryRow(ctx, `
		select aa.id, aa.attempt_id, aa.question_id, aa.response_text, aa.selected_option_ids,
		       aa.is_correct, aa.points_awarded, aa.auto_graded, aa.feedback, aa.graded_at
		from attempt_answers aa where aa.org_id = $1 and aa.id = $2`, orgID, answerID).
		Scan(&a.ID, &a.AttemptID, &a.QuestionID, &a.ResponseText, &a.SelectedOptionIDs,
			&a.IsCorrect, &a.PointsAwarded, &a.AutoGraded, &a.Feedback, &a.GradedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// ---------------------------------------------------------------------
// Attempt lifecycle
// ---------------------------------------------------------------------

// MarkAttemptSubmitted closes the learner's part of an attempt. The
// status is pending_review when manual questions remain, otherwise the
// score is finalised immediately.
func (db *DB) MarkAttemptSubmitted(ctx context.Context, orgID, attemptID uuid.UUID, autoScore, autoMax, passMark float64, needsManual bool) (*domain.Attempt, error) {
	status := domain.AttemptPendingReview
	passed := (*bool)(nil)
	percent := 0.0
	if !needsManual {
		percent = percentOf(autoScore, autoMax)
		p := percent >= passMark
		passed = &p
		if p {
			status = domain.AttemptPassed
		} else {
			status = domain.AttemptFailed
		}
	}
	_, err := db.pool.Exec(ctx, `
		update attempts set status = $1, submitted_at = now(), score = $2,
		       max_score = $3, percent = $4, passed = $5
		where org_id = $6 and id = $7`,
		status, autoScore, autoMax, percent, passed, orgID, attemptID)
	if err != nil {
		return nil, mapErr(err)
	}
	return db.AttemptDetail(ctx, orgID, attemptID)
}

// FinalizeGradedAttempt recomputes the totals once manual grading is
// complete and applies the pass mark.
func (db *DB) FinalizeGradedAttempt(ctx context.Context, orgID, attemptID uuid.UUID, passMark float64) (*domain.Attempt, error) {
	score, maxScore, err := db.attemptTotals(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	percent := percentOf(score, maxScore)
	passed := percent >= passMark
	status := domain.AttemptFailed
	if passed {
		status = domain.AttemptPassed
	}
	if _, err := db.pool.Exec(ctx, `
		update attempts set status = $1, score = $2, max_score = $3, percent = $4,
		       passed = $5, graded_at = now()
		where org_id = $6 and id = $7`,
		status, score, maxScore, percent, passed, orgID, attemptID); err != nil {
		return nil, mapErr(err)
	}
	return db.AttemptDetail(ctx, orgID, attemptID)
}

// attemptTotals sums the points awarded against the points available.
//
// The denominator is deliberately derived from attempt_answers rather than
// from the whole question set: a question that was never answered must not
// inflate the maximum and quietly make a pass impossible. Unanswered
// required questions are handled at submission time, which is where an
// incomplete paper is actually rejected.
func (db *DB) attemptTotals(ctx context.Context, orgID, attemptID uuid.UUID) (score, maxScore float64, err error) {
	err = db.pool.QueryRow(ctx, `
		select coalesce(sum(coalesce(aa.points_awarded, 0)), 0),
		       coalesce(sum(q.points), 0)
		from attempt_answers aa
		join questions q on q.id = aa.question_id
		where aa.org_id = $1 and aa.attempt_id = $2`, orgID, attemptID).
		Scan(&score, &maxScore)
	return score, maxScore, mapErr(err)
}

// ApplyAttemptOutcome writes the learner's final score and status back to
// the assignment, and marks it passed when the attempt passed.
func (db *DB) ApplyAttemptOutcome(ctx context.Context, orgID, assignmentID uuid.UUID, percent float64, passed bool) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		var better bool
		if err := tx.QueryRow(ctx, `
			select coalesce(best_score, -1) < $1 from assignments
			where org_id = $2 and id = $3`, percent, orgID, assignmentID).Scan(&better); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `
			update assignments set
				best_score = greatest(coalesce(best_score, 0), $1),
				final_score = $1,
				status = case when $2 then 'passed' else
				             case when status in ('not_started') then 'in_progress' else status end end,
				passed_at = case when $2 then coalesce(passed_at, now()) else passed_at end,
				completed_at = case when $2 then coalesce(completed_at, now()) else completed_at end,
				last_activity_at = now(),
				updated_at = now()
			where org_id = $3 and id = $4`, percent, passed, orgID, assignmentID); err != nil {
			return mapErr(err)
		}
		// A learner who exhausts every attempt and still has not passed is
		// recorded as failed rather than left mid-flow.
		if !passed {
			if _, err := tx.Exec(ctx, `
				update assignments set status = 'failed', updated_at = now()
				where org_id = $1 and id = $2
				  and status not in ('passed')
				  and attempts_used >= (select max_attempts from campaigns c
				        join assignments a2 on a2.campaign_id = c.id
				        where a2.id = $2)`, orgID, assignmentID); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

func percentOf(score, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return round2(score / max * 100)
}

// ---------------------------------------------------------------------
// Reminders
// ---------------------------------------------------------------------

func (db *DB) CreateReminder(ctx context.Context, orgID uuid.UUID, r domain.Reminder) (*domain.Reminder, error) {
	var out domain.Reminder
	err := db.pool.QueryRow(ctx, `
		insert into reminders (org_id, assignment_id, learner_id, campaign_id, channel, subject, body, sent_by)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning id, assignment_id, learner_id, campaign_id, channel, subject, body, sent_by, created_at`,
		orgID, r.AssignmentID, r.LearnerID, r.CampaignID, r.Channel, r.Subject, r.Body, r.SentBy).
		Scan(&out.ID, &out.AssignmentID, &out.LearnerID, &out.CampaignID, &out.Channel,
			&out.Subject, &out.Body, &out.SentBy, &out.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &out, nil
}

func (db *DB) ListReminders(ctx context.Context, orgID uuid.UUID, page, perPage int) ([]domain.Reminder, int, error) {
	base := `select r.id, r.assignment_id, r.learner_id, p.full_name, r.campaign_id, r.channel,
			r.subject, r.body, r.sent_by, r.created_at
		from reminders r left join profiles p on p.id = r.learner_id
		where r.org_id = $1 order by r.created_at desc`
	countSQL := `select count(*) from reminders where org_id = $1`

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, []any{orgID}, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Reminder{}
	for rows.Next() {
		var rm domain.Reminder
		if err := rows.Scan(&rm.ID, &rm.AssignmentID, &rm.LearnerID, &rm.LearnerName,
			&rm.CampaignID, &rm.Channel, &rm.Subject, &rm.Body, &rm.SentBy, &rm.CreatedAt); err != nil {
			return nil, 0, mapErr(err)
		}
		out = append(out, rm)
	}
	return out, total, mapErr(rows.Err())
}

// MarkReminderSent flags that an overdue learner has been chased, so the
// dashboard does not keep nagging.
func (db *DB) MarkReminderSent(ctx context.Context, assignmentID uuid.UUID) error {
	_, err := db.pool.Exec(ctx,
		`update assignments set overdue_notified_at = now() where id = $1`, assignmentID)
	return mapErr(err)
}

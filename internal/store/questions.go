package store

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

const questionCols = `
	q.id, q.course_id, q.lesson_id, q.org_id, q.type, q.prompt, q.hint, q.explanation,
	q.points, q.position, q.min_length, q.max_length, q.is_required,
	q.created_at, q.updated_at`

func scanQuestion(row interface{ Scan(...any) error }) (*domain.Question, error) {
	var q domain.Question
	err := row.Scan(&q.ID, &q.CourseID, &q.LessonID, &q.OrgID, &q.Type, &q.Prompt,
		&q.Hint, &q.Explanation, &q.Points, &q.Position, &q.MinLength, &q.MaxLength,
		&q.IsRequired, &q.CreatedAt, &q.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &q, nil
}

// QuestionsForCourse returns every question with its options. Callers that
// are serving a learner must strip IsCorrect and Explanation; the service
// layer does that explicitly so it is visible in review.
func (db *DB) QuestionsForCourse(ctx context.Context, orgID, courseID uuid.UUID) ([]domain.Question, error) {
	return db.questionsWhere(ctx,
		`q.org_id = $1 and q.course_id = $2 order by q.position, q.created_at`, orgID, courseID)
}

func (db *DB) QuestionsForLesson(ctx context.Context, orgID, lessonID uuid.UUID) ([]domain.Question, error) {
	return db.questionsWhere(ctx,
		`q.org_id = $1 and q.lesson_id = $2 order by q.position, q.created_at`, orgID, lessonID)
}

func (db *DB) questionsWhere(ctx context.Context, where string, args ...any) ([]domain.Question, error) {
	rows, err := db.pool.Query(ctx, `select `+questionCols+` from questions q where `+where, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []domain.Question{}
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		byID[q.ID] = len(out)
		out = append(out, *q)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	if len(out) == 0 {
		return out, nil
	}

	// One extra round trip for all options of all questions.
	ids := make([]uuid.UUID, 0, len(out))
	for _, q := range out {
		ids = append(ids, q.ID)
	}
	optRows, err := db.pool.Query(ctx, `
		select o.id, o.question_id, o.label, o.is_correct, o.position
		from question_options o where o.question_id = any($1)
		order by o.position, o.created_at`, ids)
	if err != nil {
		return nil, mapErr(err)
	}
	defer optRows.Close()
	for optRows.Next() {
		var o domain.Option
		if err := optRows.Scan(&o.ID, &o.QuestionID, &o.Label, &o.IsCorrect, &o.Position); err != nil {
			return nil, mapErr(err)
		}
		if idx, ok := byID[o.QuestionID]; ok {
			out[idx].Options = append(out[idx].Options, o)
		}
	}
	for i := range out {
		if out[i].Options == nil {
			out[i].Options = []domain.Option{}
		}
	}
	return out, mapErr(optRows.Err())
}

func (db *DB) QuestionByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Question, error) {
	q, err := scanQuestion(db.pool.QueryRow(ctx,
		`select `+questionCols+` from questions q where q.org_id = $1 and q.id = $2`, orgID, id))
	if err != nil {
		return nil, err
	}
	opts, err := db.questionOptions(ctx, id)
	if err != nil {
		return nil, err
	}
	q.Options = opts
	return q, nil
}

func (db *DB) questionOptions(ctx context.Context, questionID uuid.UUID) ([]domain.Option, error) {
	rows, err := db.pool.Query(ctx, `
		select id, question_id, label, is_correct, position
		from question_options where question_id = $1 order by position, created_at`, questionID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Option{}
	for rows.Next() {
		var o domain.Option
		if err := rows.Scan(&o.ID, &o.QuestionID, &o.Label, &o.IsCorrect, &o.Position); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, o)
	}
	return out, mapErr(rows.Err())
}

type CreateQuestionParams struct {
	CourseID    uuid.UUID
	LessonID    *uuid.UUID
	OrgID       uuid.UUID
	Type        domain.QuestionType
	Prompt      string
	Hint        *string
	Explanation *string
	Points      float64
	MinLength   *int
	MaxLength   *int
	IsRequired  bool
	Options     []CreateOptionParams
	Position    int
}

type CreateOptionParams struct {
	Label     string
	IsCorrect bool
}

// CreateQuestion inserts a question and its options atomically, so a
// choice question can never be stored without its options.
func (db *DB) CreateQuestion(ctx context.Context, p CreateQuestionParams) (*domain.Question, error) {
	if p.Points <= 0 {
		p.Points = 1
	}
	if p.Position <= 0 {
		p.Position = db.nextPosition(ctx,
			`select coalesce(max(position),0)+1 from questions where course_id = $1`, p.CourseID)
	}
	var out *domain.Question
	err := db.inTx(ctx, func(tx pgxTx) error {
		var q domain.Question
		if err := tx.QueryRow(ctx, `
			insert into questions (course_id, lesson_id, org_id, type, prompt, hint, explanation,
			                       points, position, min_length, max_length, is_required)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			returning `+questionCols,
			p.CourseID, p.LessonID, p.OrgID, p.Type, p.Prompt, p.Hint, p.Explanation,
			p.Points, p.Position, p.MinLength, p.MaxLength, p.IsRequired).
			Scan(&q.ID, &q.CourseID, &q.LessonID, &q.OrgID, &q.Type, &q.Prompt, &q.Hint,
				&q.Explanation, &q.Points, &q.Position, &q.MinLength, &q.MaxLength,
				&q.IsRequired, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return mapErr(err)
		}
		for i, o := range p.Options {
			var opt domain.Option
			if err := tx.QueryRow(ctx, `
				insert into question_options (question_id, org_id, label, is_correct, position)
				values ($1,$2,$3,$4,$5)
				returning id, question_id, label, is_correct, position`,
				q.ID, p.OrgID, o.Label, o.IsCorrect, i+1).
				Scan(&opt.ID, &opt.QuestionID, &opt.Label, &opt.IsCorrect, &opt.Position); err != nil {
				return mapErr(err)
			}
			q.Options = append(q.Options, opt)
		}
		out = &q
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type UpdateQuestionParams struct {
	Prompt      *string
	Hint        *string
	Explanation *string
	Points      *float64
	Position    *int
	MinLength   *int
	MaxLength   *int
	IsRequired  *bool
	Type        *domain.QuestionType
	// Options replaces the option set wholesale when non-nil.
	Options *[]CreateOptionParams
}

func (db *DB) UpdateQuestion(ctx context.Context, orgID, id uuid.UUID, p UpdateQuestionParams) (*domain.Question, error) {
	err := db.inTx(ctx, func(tx pgxTx) error {
		set := []string{"updated_at = now()"}
		args := []any{orgID, id}
		add := func(col string, v any) {
			args = append(args, v)
			set = append(set, col+" = $"+itoa(len(args)))
		}
		if p.Prompt != nil {
			add("prompt", *p.Prompt)
		}
		if p.Hint != nil {
			add("hint", *p.Hint)
		}
		if p.Explanation != nil {
			add("explanation", *p.Explanation)
		}
		if p.Points != nil {
			add("points", *p.Points)
		}
		if p.Position != nil {
			add("position", *p.Position)
		}
		if p.MinLength != nil {
			add("min_length", *p.MinLength)
		}
		if p.MaxLength != nil {
			add("max_length", *p.MaxLength)
		}
		if p.IsRequired != nil {
			add("is_required", *p.IsRequired)
		}
		if p.Type != nil {
			add("type", *p.Type)
		}
		q := `update questions set ` + strings.Join(set, ", ") +
			` where org_id = $1 and id = $2`
		if len(set) > 1 {
			if _, err := tx.Exec(ctx, q, args...); err != nil {
				return mapErr(err)
			}
		}

		if p.Options != nil {
			if _, err := tx.Exec(ctx, `delete from question_options where question_id = $1`, id); err != nil {
				return mapErr(err)
			}
			for i, o := range *p.Options {
				if _, err := tx.Exec(ctx, `
					insert into question_options (question_id, org_id, label, is_correct, position)
					values ($1,$2,$3,$4,$5)`, id, orgID, o.Label, o.IsCorrect, i+1); err != nil {
					return mapErr(err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db.QuestionByID(ctx, orgID, id)
}

func (db *DB) DeleteQuestion(ctx context.Context, orgID, id uuid.UUID) error {
	// A question already answered in a submitted attempt is part of the
	// audit trail and cannot be removed.
	var answered int
	if err := db.pool.QueryRow(ctx, `
		select count(*) from attempt_answers where question_id = $1`, id).Scan(&answered); err != nil {
		return mapErr(err)
	}
	if answered > 0 {
		return ErrConflict
	}
	tag, err := db.pool.Exec(ctx, `delete from questions where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderQuestions writes an explicit question order.
func (db *DB) ReorderQuestions(ctx context.Context, orgID, courseID uuid.UUID, orderedIDs []uuid.UUID) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		for i, id := range orderedIDs {
			if _, err := tx.Exec(ctx, `
				update questions set position = $1, updated_at = now()
				where id = $2 and org_id = $3 and course_id = $4`, i+1, id, orgID, courseID); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// CorrectOptionIDs returns the correct option ids per question, used for
// auto-grading a submitted attempt.
func (db *DB) CorrectOptionIDs(ctx context.Context, questionIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	if len(questionIDs) == 0 {
		return out, nil
	}
	rows, err := db.pool.Query(ctx, `
		select question_id, id from question_options
		where question_id = any($1) and is_correct`, questionIDs)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var qid, oid uuid.UUID
		if err := rows.Scan(&qid, &oid); err != nil {
			return nil, mapErr(err)
		}
		out[qid] = append(out[qid], oid)
	}
	return out, mapErr(rows.Err())
}

// CourseTotalPoints is the denominator used to turn a raw score into a
// percentage.
func (db *DB) CourseTotalPoints(ctx context.Context, courseID uuid.UUID) (float64, error) {
	var total *float64
	err := db.pool.QueryRow(ctx,
		`select sum(points) from questions where course_id = $1`, courseID).Scan(&total)
	if err != nil {
		return 0, mapErr(err)
	}
	if total == nil {
		return 0, nil
	}
	return float64(*total), nil
}

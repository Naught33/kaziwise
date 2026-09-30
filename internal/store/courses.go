package store

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

const courseCols = `
	c.id, c.org_id, c.title, c.code, c.description, c.category, c.cover_asset_id,
	c.status, c.estimated_minutes, c.created_by, c.published_at, c.created_at, c.updated_at`

// Course aggregates used by the library grid.
const courseAggs = `
	coalesce((select count(*) from modules m where m.course_id = c.id), 0)::int as module_count,
	coalesce((select count(*) from lessons l where l.course_id = c.id), 0)::int as lesson_count,
	coalesce((select count(*) from blocks bk join lessons l2 on l2.id = bk.lesson_id
	           where l2.course_id = c.id), 0)::int as block_count,
	coalesce((select count(*) from questions q where q.course_id = c.id), 0)::int as question_count,
	coalesce((select sum(q.points) from questions q where q.course_id = c.id), 0) as total_points,
	coalesce((select sum(l.estimated_minutes) from lessons l where l.course_id = c.id), 0)::int as duration_minutes,
	coalesce((select count(*) from assignments a where a.course_id = c.id), 0)::int as assigned_learners,
	-- "Completed" is learning completion (completed_at is stamped by the
	-- lesson rollup) or a passed assessment. There is no 'completed' value
	-- in the assignment_status enum, so it is derived rather than compared.
	coalesce((select count(*) from assignments a where a.course_id = c.id
	           and (a.completed_at is not null or a.status = 'passed')), 0)::int as completed_learners`

func scanCourse(row interface{ Scan(...any) error }) (*domain.Course, error) {
	var c domain.Course
	err := row.Scan(&c.ID, &c.OrgID, &c.Title, &c.Code, &c.Description, &c.Category,
		&c.CoverAssetID, &c.Status, &c.EstimatedMinutes, &c.CreatedBy, &c.PublishedAt,
		&c.CreatedAt, &c.UpdatedAt,
		&c.ModuleCount, &c.LessonCount, &c.BlockCount, &c.QuestionCount, &c.TotalPoints,
		&c.DurationMinutes, &c.AssignedLearners, &c.CompletedLearners)
	if err != nil {
		return nil, mapErr(err)
	}
	if c.AssignedLearners > 0 {
		c.CompletionPct = round2(float64(c.CompletedLearners) / float64(c.AssignedLearners) * 100)
	}
	return &c, nil
}

type CreateCourseParams struct {
	OrgID            uuid.UUID
	Title            string
	Code             *string
	Description      *string
	Category         *string
	CoverAssetID     *uuid.UUID
	EstimatedMinutes int
	CreatedBy        *uuid.UUID
}

func (db *DB) CreateCourse(ctx context.Context, p CreateCourseParams) (*domain.Course, error) {
	var id uuid.UUID
	if err := db.pool.QueryRow(ctx,
		`insert into courses (org_id, title, code, description, category, cover_asset_id,
		                      estimated_minutes, created_by)
		 values ($1,$2,$3,$4,$5,$6,$7,$8) returning id`,
		p.OrgID, p.Title, p.Code, p.Description, p.Category, p.CoverAssetID,
		p.EstimatedMinutes, p.CreatedBy).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	return db.CourseByID(ctx, p.OrgID, id)
}

type UpdateCourseParams struct {
	Title            *string
	Code             *string
	Description      *string
	Category         *string
	CoverAssetID     *uuid.UUID
	ClearCover       bool
	EstimatedMinutes *int
	Status           *domain.CourseStatus
}

func (db *DB) UpdateCourse(ctx context.Context, orgID, id uuid.UUID, p UpdateCourseParams) (*domain.Course, error) {
	set := []string{"updated_at = now()"}
	args := []any{orgID, id}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, col+" = $"+itoa(len(args)))
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Code != nil {
		add("code", *p.Code)
	}
	if p.Description != nil {
		add("description", *p.Description)
	}
	if p.Category != nil {
		add("category", *p.Category)
	}
	if p.CoverAssetID != nil {
		add("cover_asset_id", *p.CoverAssetID)
	}
	if p.ClearCover {
		set = append(set, "cover_asset_id = null")
	}
	if p.EstimatedMinutes != nil {
		add("estimated_minutes", *p.EstimatedMinutes)
	}
	if p.Status != nil {
		add("status", *p.Status)
		set = append(set, "published_at = case when $"+itoa(len(args))+" = 'published' and published_at is null then now() else published_at end")
		args = append(args, *p.Status)
	}
	q := `update courses set ` + strings.Join(set, ", ") +
		` where org_id = $1 and id = $2 returning id`
	var newID uuid.UUID
	if err := db.pool.QueryRow(ctx, q, args...).Scan(&newID); err != nil {
		return nil, mapErr(err)
	}
	return db.CourseByID(ctx, orgID, newID)
}

func (db *DB) CourseByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Course, error) {
	return scanCourse(db.pool.QueryRow(ctx,
		`select `+courseCols+`, `+courseAggs+` from courses c where c.org_id = $1 and c.id = $2`,
		orgID, id))
}

func (db *DB) CourseByCode(ctx context.Context, orgID uuid.UUID, code string) (*domain.Course, error) {
	return scanCourse(db.pool.QueryRow(ctx,
		`select `+courseCols+`, `+courseAggs+` from courses c
		 where c.org_id = $1 and c.code = $2`, orgID, code))
}

type CourseListFilter struct {
	Search   string
	Status   string
	Category string
	Page     int
	PerPage  int
	Sort     string
	Order    string
}

func (db *DB) ListCourses(ctx context.Context, orgID uuid.UUID, f CourseListFilter) ([]domain.Course, int, error) {
	b := orgScope(orgID)
	for _, col := range []string{"org_id", "title", "code", "status", "category"} {
		b.where = prefixCols(b.where, col)
	}
	if f.Search != "" {
		i := len(b.values()) + 1
		b.add("(c.title ilike $" + itoa(i) + " or coalesce(c.description,'') ilike $" + itoa(i) + " or coalesce(c.code,'') ilike $" + itoa(i) + ")")
		b.args = append(b.args, "%"+f.Search+"%")
	}
	if f.Status != "" {
		b.add("c.status = $" + itoa(len(b.values())+1))
		b.args = append(b.args, f.Status)
	}
	if f.Category != "" {
		b.add("c.category = $" + itoa(len(b.values())+1))
		b.args = append(b.args, f.Category)
	}

	base := `select ` + courseCols + `, ` + courseAggs + ` from courses c` + b.whereClause()
	countSQL := `select count(*) from courses c` + b.whereClause()

	sortCol := map[string]string{
		"": "c.updated_at", "title": "c.title", "created": "c.created_at",
		"status": "c.status", "published": "c.published_at",
	}[f.Sort]
	if sortCol == "" {
		sortCol = "c.updated_at"
	}
	order := "desc"
	if strings.EqualFold(f.Order, "asc") {
		order = "asc"
	}
	base += " order by " + sortCol + " " + order + ", c.id asc"

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Course{}
	for rows.Next() {
		c, err := scanCourse(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, mapErr(rows.Err())
}

// DeleteCourse refuses while the course is running in a campaign, so
// learner assignments and issued certificates keep their referents.
func (db *DB) DeleteCourse(ctx context.Context, orgID, id uuid.UUID) error {
	var active int
	if err := db.pool.QueryRow(ctx, `
		select count(*) from campaigns c
		where c.course_id = $1 and c.status <> 'closed'`, id).Scan(&active); err != nil {
		return mapErr(err)
	}
	if active > 0 {
		return ErrConflict
	}
	tag, err := db.pool.Exec(ctx, `delete from courses where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------
// Modules
// ---------------------------------------------------------------------

const moduleCols = `id, course_id, org_id, title, description, position, created_at, updated_at`

func scanModule(row interface{ Scan(...any) error }) (*domain.Module, error) {
	var m domain.Module
	err := row.Scan(&m.ID, &m.CourseID, &m.OrgID, &m.Title, &m.Description,
		&m.Position, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

func (db *DB) ListModules(ctx context.Context, orgID, courseID uuid.UUID) ([]domain.Module, error) {
	rows, err := db.pool.Query(ctx, `
		select `+moduleCols+`,
		       coalesce((select count(*) from lessons l where l.module_id = m.id), 0)::int as lesson_count,
		       coalesce((select sum(l.estimated_minutes) from lessons l where l.module_id = m.id), 0)::int as duration_minutes
		from modules m where m.org_id = $1 and m.course_id = $2
		order by m.position, m.created_at`, orgID, courseID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Module{}
	for rows.Next() {
		var m domain.Module
		if err := rows.Scan(&m.ID, &m.CourseID, &m.OrgID, &m.Title, &m.Description,
			&m.Position, &m.CreatedAt, &m.UpdatedAt, &m.LessonCount, &m.DurationMinutes); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, m)
	}
	return out, mapErr(rows.Err())
}

func (db *DB) ModuleByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Module, error) {
	return scanModule(db.pool.QueryRow(ctx,
		`select `+moduleCols+` from modules m where m.org_id = $1 and m.id = $2`, orgID, id))
}

func (db *DB) CreateModule(ctx context.Context, orgID, courseID uuid.UUID, title string, description *string, position int) (*domain.Module, error) {
	if position <= 0 {
		position = db.nextPosition(ctx, `select coalesce(max(position),0)+1 from modules where course_id = $1`, courseID)
	}
	var m domain.Module
	err := db.pool.QueryRow(ctx, `
		insert into modules (course_id, org_id, title, description, position)
		values ($1,$2,$3,$4,$5)
		returning `+moduleCols,
		courseID, orgID, title, description, position).
		Scan(&m.ID, &m.CourseID, &m.OrgID, &m.Title, &m.Description, &m.Position,
			&m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

type UpdateModuleParams struct {
	Title       *string
	Description *string
	Position    *int
}

func (db *DB) UpdateModule(ctx context.Context, orgID, id uuid.UUID, p UpdateModuleParams) (*domain.Module, error) {
	set := []string{"updated_at = now()"}
	args := []any{orgID, id}
	if p.Title != nil {
		args = append(args, *p.Title)
		set = append(set, "title = $"+itoa(len(args)))
	}
	if p.Description != nil {
		args = append(args, *p.Description)
		set = append(set, "description = $"+itoa(len(args)))
	}
	if p.Position != nil {
		args = append(args, *p.Position)
		set = append(set, "position = $"+itoa(len(args)))
	}
	q := `update modules set ` + strings.Join(set, ", ") + ` where org_id = $1 and id = $2 returning ` + moduleCols
	return scanModule(db.pool.QueryRow(ctx, q, args...))
}

func (db *DB) DeleteModule(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := db.pool.Exec(ctx, `delete from modules where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------
// Lessons
// ---------------------------------------------------------------------

// lessonCols is unqualified because the create path returns it from an
// INSERT, where no table alias is in scope.
const lessonCols = `
	id, module_id, course_id, org_id, title, summary, kind, position,
	estimated_minutes, created_at, updated_at`

// lessonColsJoined is the same projection under the alias the read paths use.
const lessonColsJoined = `
	l.id, l.module_id, l.course_id, l.org_id, l.title, l.summary, l.kind, l.position,
	l.estimated_minutes, l.created_at, l.updated_at`

func scanLesson(row interface{ Scan(...any) error }) (*domain.Lesson, error) {
	var l domain.Lesson
	err := row.Scan(&l.ID, &l.ModuleID, &l.CourseID, &l.OrgID, &l.Title, &l.Summary,
		&l.Kind, &l.Position, &l.EstimatedMinutes, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

func (db *DB) ListLessons(ctx context.Context, orgID, courseID uuid.UUID) ([]domain.Lesson, error) {
	rows, err := db.pool.Query(ctx, `
		select `+lessonColsJoined+`,
		       coalesce((select count(*) from blocks b where b.lesson_id = l.id), 0)::int as block_count,
		       coalesce((select count(*) from questions q where q.lesson_id = l.id), 0)::int as question_count
		from lessons l where l.org_id = $1 and l.course_id = $2
		order by l.position, l.created_at`, orgID, courseID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Lesson{}
	for rows.Next() {
		var l domain.Lesson
		if err := rows.Scan(&l.ID, &l.ModuleID, &l.CourseID, &l.OrgID, &l.Title, &l.Summary,
			&l.Kind, &l.Position, &l.EstimatedMinutes, &l.CreatedAt, &l.UpdatedAt,
			&l.BlockCount, &l.QuestionCount); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, l)
	}
	return out, mapErr(rows.Err())
}

func (db *DB) LessonByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Lesson, error) {
	return scanLesson(db.pool.QueryRow(ctx,
		`select `+lessonColsJoined+` from lessons l where l.org_id = $1 and l.id = $2`, orgID, id))
}

type CreateLessonParams struct {
	ModuleID         uuid.UUID
	CourseID         uuid.UUID
	OrgID            uuid.UUID
	Title            string
	Summary          *string
	Kind             domain.LessonKind
	EstimatedMinutes int
	Position         int
}

func (db *DB) CreateLesson(ctx context.Context, p CreateLessonParams) (*domain.Lesson, error) {
	if p.Kind == "" {
		p.Kind = domain.LessonContent
	}
	if p.Position <= 0 {
		p.Position = db.nextPosition(ctx,
			`select coalesce(max(position),0)+1 from lessons where module_id = $1`, p.ModuleID)
	}
	var l domain.Lesson
	err := db.pool.QueryRow(ctx, `
		insert into lessons (module_id, course_id, org_id, title, summary, kind, position, estimated_minutes)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning `+lessonCols,
		p.ModuleID, p.CourseID, p.OrgID, p.Title, p.Summary, p.Kind, p.Position, p.EstimatedMinutes).
		Scan(&l.ID, &l.ModuleID, &l.CourseID, &l.OrgID, &l.Title, &l.Summary, &l.Kind,
			&l.Position, &l.EstimatedMinutes, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

type UpdateLessonParams struct {
	Title            *string
	Summary          *string
	Kind             *domain.LessonKind
	Position         *int
	EstimatedMinutes *int
}

func (db *DB) UpdateLesson(ctx context.Context, orgID, id uuid.UUID, p UpdateLessonParams) (*domain.Lesson, error) {
	set := []string{"updated_at = now()"}
	args := []any{orgID, id}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, col+" = $"+itoa(len(args)))
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Summary != nil {
		add("summary", *p.Summary)
	}
	if p.Kind != nil {
		add("kind", *p.Kind)
	}
	if p.Position != nil {
		add("position", *p.Position)
	}
	if p.EstimatedMinutes != nil {
		add("estimated_minutes", *p.EstimatedMinutes)
	}
	q := `update lessons set ` + strings.Join(set, ", ") + ` where org_id = $1 and id = $2 returning ` + lessonCols
	return scanLesson(db.pool.QueryRow(ctx, q, args...))
}

func (db *DB) DeleteLesson(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := db.pool.Exec(ctx, `delete from lessons where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------------

// blockCols is deliberately unqualified. The create path uses it in an
// INSERT ... RETURNING, where no table alias is in scope, and a qualified
// projection there fails with "missing FROM-clause entry".
const blockCols = `
	id, lesson_id, org_id, type, position, title, body, asset_id,
	page_from, page_to, chapter_from, chapter_to, created_at, updated_at`

// blockColsJoined is the same projection qualified with the alias the read
// paths join under.
const blockColsJoined = `
	b.id, b.lesson_id, b.org_id, b.type, b.position, b.title, b.body, b.asset_id,
	b.page_from, b.page_to, b.chapter_from, b.chapter_to, b.created_at, b.updated_at`

func scanBlock(row interface{ Scan(...any) error }) (*domain.Block, error) {
	var b domain.Block
	err := row.Scan(&b.ID, &b.LessonID, &b.OrgID, &b.Type, &b.Position, &b.Title, &b.Body,
		&b.AssetID, &b.PageFrom, &b.PageTo, &b.ChapterFrom, &b.ChapterTo,
		&b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &b, nil
}

func (db *DB) ListBlocks(ctx context.Context, orgID, lessonID uuid.UUID) ([]domain.Block, error) {
	rows, err := db.pool.Query(ctx,
		`select `+blockColsJoined+` from blocks b where b.org_id = $1 and b.lesson_id = $2
		 order by b.position, b.created_at`, orgID, lessonID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Block{}
	for rows.Next() {
		bl, err := scanBlock(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *bl)
	}
	return out, mapErr(rows.Err())
}

func (db *DB) BlockByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Block, error) {
	return scanBlock(db.pool.QueryRow(ctx,
		`select `+blockColsJoined+` from blocks b where b.org_id = $1 and b.id = $2`, orgID, id))
}

type CreateBlockParams struct {
	LessonID    uuid.UUID
	OrgID       uuid.UUID
	Type        domain.BlockType
	Title       *string
	Body        *string
	AssetID     *uuid.UUID
	PageFrom    *int
	PageTo      *int
	ChapterFrom *int
	ChapterTo   *int
}

func (db *DB) CreateBlock(ctx context.Context, p CreateBlockParams) (*domain.Block, error) {
	pos := db.nextPosition(ctx,
		`select coalesce(max(position),0)+1 from blocks where lesson_id = $1`, p.LessonID)
	var b domain.Block
	err := db.pool.QueryRow(ctx, `
		insert into blocks (lesson_id, org_id, type, position, title, body, asset_id,
		                    page_from, page_to, chapter_from, chapter_to)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		returning `+blockCols,
		p.LessonID, p.OrgID, p.Type, pos, p.Title, p.Body, p.AssetID,
		p.PageFrom, p.PageTo, p.ChapterFrom, p.ChapterTo).
		Scan(&b.ID, &b.LessonID, &b.OrgID, &b.Type, &b.Position, &b.Title, &b.Body,
			&b.AssetID, &b.PageFrom, &b.PageTo, &b.ChapterFrom, &b.ChapterTo,
			&b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &b, nil
}

type UpdateBlockParams struct {
	Type       *domain.BlockType
	Title      *string
	Body       *string
	AssetID    *uuid.UUID
	ClearAsset bool
	PageFrom   *int
	PageTo     *int
	Position   *int
}

func (db *DB) UpdateBlock(ctx context.Context, orgID, id uuid.UUID, p UpdateBlockParams) (*domain.Block, error) {
	set := []string{"updated_at = now()"}
	args := []any{orgID, id}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, col+" = $"+itoa(len(args)))
	}
	if p.Type != nil {
		add("type", *p.Type)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Body != nil {
		add("body", *p.Body)
	}
	if p.AssetID != nil {
		add("asset_id", *p.AssetID)
	}
	if p.ClearAsset {
		set = append(set, "asset_id = null")
	}
	if p.PageFrom != nil {
		add("page_from", *p.PageFrom)
	}
	if p.PageTo != nil {
		add("page_to", *p.PageTo)
	}
	if p.Position != nil {
		add("position", *p.Position)
	}
	q := `update blocks set ` + strings.Join(set, ", ") + ` where org_id = $1 and id = $2 returning ` + blockCols
	return scanBlock(db.pool.QueryRow(ctx, q, args...))
}

func (db *DB) DeleteBlock(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := db.pool.Exec(ctx, `delete from blocks where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderBlocks writes an explicit order in one transaction.
func (db *DB) ReorderBlocks(ctx context.Context, orgID, lessonID uuid.UUID, orderedIDs []uuid.UUID) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		for i, id := range orderedIDs {
			_, err := tx.Exec(ctx, `
				update blocks set position = $1, updated_at = now()
				where id = $2 and org_id = $3 and lesson_id = $4`, i+1, id, orgID, lessonID)
			if err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// ReorderModules writes an explicit module order in one transaction.
func (db *DB) ReorderModules(ctx context.Context, orgID, courseID uuid.UUID, orderedIDs []uuid.UUID) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		for i, id := range orderedIDs {
			_, err := tx.Exec(ctx, `
				update modules set position = $1, updated_at = now()
				where id = $2 and org_id = $3 and course_id = $4`, i+1, id, orgID, courseID)
			if err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// ReorderLessons writes an explicit lesson order in one transaction.
func (db *DB) ReorderLessons(ctx context.Context, orgID, moduleID uuid.UUID, orderedIDs []uuid.UUID) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		for i, id := range orderedIDs {
			_, err := tx.Exec(ctx, `
				update lessons set position = $1, updated_at = now()
				where id = $2 and org_id = $3 and module_id = $4`, i+1, id, orgID, moduleID)
			if err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------
// Outline helpers
// ---------------------------------------------------------------------

// CourseOutline assembles the full course -> modules -> lessons -> blocks
// tree used by the Course Builder and the course player.
func (db *DB) CourseOutline(ctx context.Context, orgID, courseID uuid.UUID) (*domain.CourseOutline, error) {
	course, err := db.CourseByID(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	modules, err := db.ListModules(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	lessons, err := db.ListLessons(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	byModule := map[uuid.UUID][]domain.Lesson{}
	for _, l := range lessons {
		byModule[l.ModuleID] = append(byModule[l.ModuleID], l)
	}
	for i := range modules {
		modules[i].Lessons = byModule[modules[i].ID]
		if modules[i].Lessons == nil {
			modules[i].Lessons = []domain.Lesson{}
		}
	}
	blocks, err := db.blocksForCourse(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	byLesson := map[uuid.UUID][]domain.Block{}
	for _, b := range blocks {
		byLesson[b.LessonID] = append(byLesson[b.LessonID], b)
	}
	stats := domain.OutlineStats{Modules: len(modules), Lessons: len(lessons), Blocks: len(blocks)}
	for i := range modules {
		for j := range modules[i].Lessons {
			ls := &modules[i].Lessons[j]
			ls.Blocks = byLesson[ls.ID]
			if ls.Blocks == nil {
				ls.Blocks = []domain.Block{}
			}
		}
	}
	return &domain.CourseOutline{
		Course:  *course,
		Modules: modules,
		Stats: domain.OutlineStats{
			Modules:         stats.Modules,
			Lessons:         stats.Lessons,
			Blocks:          stats.Blocks,
			Questions:       course.QuestionCount,
			TotalPoints:     course.TotalPoints,
			DurationMinutes: course.DurationMinutes,
		},
	}, nil
}

func (db *DB) blocksForCourse(ctx context.Context, orgID, courseID uuid.UUID) ([]domain.Block, error) {
	rows, err := db.pool.Query(ctx, `
		select `+blockColsJoined+` from blocks b
		join lessons l on l.id = b.lesson_id
		where b.org_id = $1 and l.course_id = $2
		order by l.position, b.position`, orgID, courseID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.Block{}
	for rows.Next() {
		bl, err := scanBlock(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *bl)
	}
	return out, mapErr(rows.Err())
}

// ContentLessons returns only the lessons a learner must work through,
// i.e. those with at least one content block. Assessment-only lessons are
// excluded from the learning requirement.
func (db *DB) ContentLessons(ctx context.Context, orgID, courseID uuid.UUID) ([]domain.Lesson, error) {
	lessons, err := db.ListLessons(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	blocks, err := db.blocksForCourse(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	withContent := map[uuid.UUID]bool{}
	for _, b := range blocks {
		withContent[b.LessonID] = true
	}
	out := []domain.Lesson{}
	for _, l := range lessons {
		if withContent[l.ID] {
			out = append(out, l)
		}
	}
	return out, nil
}

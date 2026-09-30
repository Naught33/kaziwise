package store

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

const campaignCols = `
	c.id, c.org_id, c.course_id, c.name, c.description, c.status, c.audience_type,
	c.audience_dept, c.due_date, c.pass_mark, c.issue_certificate, c.max_attempts,
	c.require_learning, c.created_by, c.launched_at, c.closed_at, c.created_at, c.updated_at`

// campaignAggs are the counters the campaign list and dashboard show.
const campaignAggs = `
	coalesce((select count(*) from campaign_audience ca where ca.campaign_id = c.id), 0)::int as audience_count,
	coalesce((select count(*) from assignments a where a.campaign_id = c.id), 0)::int as assigned_count,
	coalesce((select count(*) from assignments a where a.campaign_id = c.id
	           and a.status in ('in_progress','pending_review','passed')), 0)::int as started_count,
	coalesce((select count(*) from assignments a where a.campaign_id = c.id
	           and a.lessons_done > 0), 0)::int as completed_count,
	coalesce((select count(*) from assignments a where a.campaign_id = c.id
	           and a.status = 'passed'), 0)::int as passed_count,
	coalesce((select count(*) from assignments a where a.campaign_id = c.id
	           and a.status = 'failed'), 0)::int as failed_count,
	coalesce((select count(*) from assignments a where a.campaign_id = c.id
	           and a.status <> 'passed' and a.due_date is not null
	           and a.due_date < current_date), 0)::int as overdue_count,
	(select round(avg(a.final_score), 2) from assignments a
	 where a.campaign_id = c.id and a.final_score is not null) as avg_score`

func scanCampaign(row interface{ Scan(...any) error }) (*domain.Campaign, error) {
	var c domain.Campaign
	err := row.Scan(&c.ID, &c.OrgID, &c.CourseID, &c.Name, &c.Description, &c.Status,
		&c.AudienceType, &c.AudienceDept, &c.DueDate, &c.PassMark, &c.IssueCertificate,
		&c.MaxAttempts, &c.RequireLearning, &c.CreatedBy, &c.LaunchedAt, &c.ClosedAt,
		&c.CreatedAt, &c.UpdatedAt,
		&c.AudienceCount, &c.AssignedCount, &c.StartedCount, &c.CompletedCount,
		&c.PassedCount, &c.FailedCount, &c.OverdueCount, &c.AvgScore)
	if err != nil {
		return nil, mapErr(err)
	}
	return &c, nil
}

// scanCampaignWithCourse consumes the trailing co.title that the campaign
// list and detail queries add for display.
func scanCampaignWithCourse(row interface{ Scan(...any) error }) (*domain.Campaign, error) {
	var c domain.Campaign
	err := row.Scan(&c.ID, &c.OrgID, &c.CourseID, &c.Name, &c.Description, &c.Status,
		&c.AudienceType, &c.AudienceDept, &c.DueDate, &c.PassMark, &c.IssueCertificate,
		&c.MaxAttempts, &c.RequireLearning, &c.CreatedBy, &c.LaunchedAt, &c.ClosedAt,
		&c.CreatedAt, &c.UpdatedAt,
		&c.AudienceCount, &c.AssignedCount, &c.StartedCount, &c.CompletedCount,
		&c.PassedCount, &c.FailedCount, &c.OverdueCount, &c.AvgScore, &c.CourseTitle)
	if err != nil {
		return nil, mapErr(err)
	}
	return &c, nil
}

type CreateCampaignParams struct {
	OrgID            uuid.UUID
	CourseID         uuid.UUID
	Name             string
	Description      *string
	AudienceType     domain.AudienceType
	AudienceDept     *string
	DueDate          *string
	PassMark         float64
	IssueCertificate bool
	MaxAttempts      int
	RequireLearning  bool
	CreatedBy        *uuid.UUID
	LearnerIDs       []uuid.UUID
}

func (db *DB) CreateCampaign(ctx context.Context, p CreateCampaignParams) (*domain.Campaign, error) {
	if p.PassMark <= 0 {
		p.PassMark = 80
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.AudienceType == "" {
		p.AudienceType = domain.AudienceAll
	}
	var out *domain.Campaign
	err := db.inTx(ctx, func(tx pgxTx) error {
		var c domain.Campaign
		if err := tx.QueryRow(ctx, `
			insert into campaigns (org_id, course_id, name, description, status, audience_type,
			                       audience_dept, due_date, pass_mark, issue_certificate,
			                       max_attempts, require_learning, created_by)
			values ($1,$2,$3,$4,'draft',$5,$6,$7,$8,$9,$10,$11,$12)
			returning `+campaignCols,
			p.OrgID, p.CourseID, p.Name, p.Description, p.AudienceType, p.AudienceDept,
			p.DueDate, p.PassMark, p.IssueCertificate, p.MaxAttempts, p.RequireLearning,
			p.CreatedBy).
			Scan(&c.ID, &c.OrgID, &c.CourseID, &c.Name, &c.Description, &c.Status,
				&c.AudienceType, &c.AudienceDept, &c.DueDate, &c.PassMark, &c.IssueCertificate,
				&c.MaxAttempts, &c.RequireLearning, &c.CreatedBy, &c.LaunchedAt, &c.ClosedAt,
				&c.CreatedAt, &c.UpdatedAt); err != nil {
			return mapErr(err)
		}
		if err := replaceAudience(ctx, tx, c.ID, p.OrgID, p.LearnerIDs); err != nil {
			return err
		}
		out = &c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db.CampaignByID(ctx, p.OrgID, out.ID)
}

func replaceAudience(ctx context.Context, tx pgxTx, campaignID, orgID uuid.UUID, learnerIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `delete from campaign_audience where campaign_id = $1`, campaignID); err != nil {
		return mapErr(err)
	}
	if len(learnerIDs) == 0 {
		return nil
	}
	b := &pgxBatch{}
	for _, id := range dedupeUUIDs(learnerIDs) {
		b.add(`insert into campaign_audience (campaign_id, profile_id, org_id)
		       values ($1,$2,$3) on conflict do nothing`, campaignID, id, orgID)
	}
	return sendAll(ctx, tx, b)
}

type UpdateCampaignParams struct {
	Name             *string
	Description      *string
	AudienceType     *domain.AudienceType
	AudienceDept     *string
	DueDate          *string
	PassMark         *float64
	IssueCertificate *bool
	MaxAttempts      *int
	RequireLearning  *bool
	LearnerIDs       *[]uuid.UUID
}

func (db *DB) UpdateCampaign(ctx context.Context, orgID, id uuid.UUID, p UpdateCampaignParams) (*domain.Campaign, error) {
	err := db.inTx(ctx, func(tx pgxTx) error {
		set := []string{"updated_at = now()"}
		args := []any{orgID, id}
		add := func(col string, v any) {
			args = append(args, v)
			set = append(set, col+" = $"+itoa(len(args)))
		}
		if p.Name != nil {
			add("name", *p.Name)
		}
		if p.Description != nil {
			add("description", *p.Description)
		}
		if p.AudienceType != nil {
			add("audience_type", *p.AudienceType)
		}
		if p.AudienceDept != nil {
			add("audience_dept", *p.AudienceDept)
		}
		if p.DueDate != nil {
			add("due_date", *p.DueDate)
		}
		if p.PassMark != nil {
			add("pass_mark", *p.PassMark)
		}
		if p.IssueCertificate != nil {
			add("issue_certificate", *p.IssueCertificate)
		}
		if p.MaxAttempts != nil {
			add("max_attempts", *p.MaxAttempts)
		}
		if p.RequireLearning != nil {
			add("require_learning", *p.RequireLearning)
		}
		if len(set) > 1 {
			q := `update campaigns set ` + strings.Join(set, ", ") + ` where org_id = $1 and id = $2`
			if _, err := tx.Exec(ctx, q, args...); err != nil {
				return mapErr(err)
			}
		}
		if p.LearnerIDs != nil {
			if err := replaceAudience(ctx, tx, id, orgID, *p.LearnerIDs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db.CampaignByID(ctx, orgID, id)
}

func (db *DB) CampaignByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Campaign, error) {
	c, err := scanCampaignWithCourse(db.pool.QueryRow(ctx, `
		select `+campaignCols+`, `+campaignAggs+`, co.title
		from campaigns c join courses co on co.id = c.course_id
		where c.org_id = $1 and c.id = $2`, orgID, id))
	if err != nil {
		return nil, err
	}
	return c, nil
}

type CampaignListFilter struct {
	Search   string
	Status   string
	CourseID *uuid.UUID
	Page     int
	PerPage  int
}

func (db *DB) ListCampaigns(ctx context.Context, orgID uuid.UUID, f CampaignListFilter) ([]domain.Campaign, int, error) {
	b := orgScope(orgID)
	b.where = prefixCols(b.where, "org_id", "name", "status")
	if f.Search != "" {
		i := len(b.values()) + 1
		b.add("(c.name ilike $" + itoa(i) + " or co.title ilike $" + itoa(i) + ")")
		b.args = append(b.args, "%"+f.Search+"%")
	}
	if f.Status != "" {
		b.add("c.status = $" + itoa(len(b.values())+1))
		b.args = append(b.args, f.Status)
	}
	if f.CourseID != nil {
		b.add("c.course_id = $" + itoa(len(b.values())+1))
		b.args = append(b.args, *f.CourseID)
	}
	base := `select ` + campaignCols + `, ` + campaignAggs + `, co.title
		from campaigns c join courses co on co.id = c.course_id` + b.whereClause() +
		` order by c.created_at desc`
	countSQL := `select count(*) from campaigns c join courses co on co.id = c.course_id` + b.whereClause()

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Campaign{}
	for rows.Next() {
		c, err := scanCampaignWithCourse(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, mapErr(rows.Err())
}

func (db *DB) DeleteCampaign(ctx context.Context, orgID, id uuid.UUID) error {
	var c domain.Campaign
	err := db.pool.QueryRow(ctx,
		`select `+campaignCols+` from campaigns c where c.org_id = $1 and c.id = $2`, orgID, id).
		Scan(&c.ID, &c.OrgID, &c.CourseID, &c.Name, &c.Description, &c.Status,
			&c.AudienceType, &c.AudienceDept, &c.DueDate, &c.PassMark, &c.IssueCertificate,
			&c.MaxAttempts, &c.RequireLearning, &c.CreatedBy, &c.LaunchedAt, &c.ClosedAt,
			&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	// Issued certificates outlive the campaign that produced them, so a
	// campaign with certificates can only be closed, never deleted.
	if c.Status == domain.CampaignActive {
		return ErrConflict
	}
	tag, err := db.pool.Exec(ctx, `delete from campaigns where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) CloseCampaign(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := db.pool.Exec(ctx, `
		update campaigns set status = 'closed', closed_at = now(), updated_at = now()
		where org_id = $1 and id = $2 and status = 'active'`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LaunchCampaign materialises one assignment row per learner. The
// audience is resolved here, in SQL, so a department-wide assignment
// cannot drift from the department filter the admin chose.
func (db *DB) LaunchCampaign(ctx context.Context, orgID, id uuid.UUID) (int, error) {
	var created int
	err := db.inTx(ctx, func(tx pgxTx) error {
		var courseID uuid.UUID
		var dueDate *string
		var audienceType, audienceDept *string
		if err := tx.QueryRow(ctx, `
			select course_id, audience_type::text, audience_dept::text, due_date::text
			from campaigns where org_id = $1 and id = $2 for update`, orgID, id).
			Scan(&courseID, &audienceType, &audienceDept, &dueDate); err != nil {
			return mapErr(err)
		}
		if audienceType == nil {
			return ErrConflict
		}

		// Placeholders are fixed: $1 org, $2 campaign, $3 course,
		// $4 due date, $5 audience department. Keeping them constant
		// across all three audience types is what stopped the
		// selected-users and department filters from reading the
		// wrong argument.
		var targetSQL string
		switch *audienceType {
		case "department":
			targetSQL = `
				select p.id from profiles p
				where p.org_id = $1 and p.status = 'active'
				  and p.role in ('learner','manager')
				  and p.department = coalesce($5, p.department)`
		case "users":
			targetSQL = `
				select ca.profile_id from campaign_audience ca
				join profiles p on p.id = ca.profile_id
				where ca.org_id = $1 and ca.campaign_id = $2
				  and p.status = 'active'`
		default: // all
			targetSQL = `
				select p.id from profiles p
				where p.org_id = $1 and p.status = 'active'
				  and p.role in ('learner','manager')`
		}

		// Only lessons that actually carry content count towards the
		// learning requirement, otherwise a placeholder lesson would
		// block a learner from reaching the assessment.
		tag, err := tx.Exec(ctx, `
			insert into assignments (org_id, campaign_id, course_id, learner_id, due_date, lessons_total)
			select $1, $2, $3, t.id, $4::date,
			       (select count(*) from lessons l
			        where l.course_id = $3
			          and exists (select 1 from blocks b where b.lesson_id = l.id))
			from (`+targetSQL+`) t
			on conflict (campaign_id, learner_id) do nothing`,
			orgID, id, courseID, dueDate, audienceDept)
		if err != nil {
			return mapErr(err)
		}
		created = int(tag.RowsAffected())

		// launched_at is only stamped on the first launch so a
		// re-launch that picks up late joiners does not rewrite history.
		if _, err := tx.Exec(ctx, `
			update campaigns
			set status = 'active',
			    launched_at = coalesce(launched_at, now()),
			    updated_at = now()
			where org_id = $1 and id = $2`, orgID, id); err != nil {
			return mapErr(err)
		}
		return nil
	})
	return created, err
}

// CampaignAudience returns the resolved learner list for a campaign.
func (db *DB) CampaignAudience(ctx context.Context, orgID, campaignID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.pool.Query(ctx, `
		select learner_id from assignments where org_id = $1 and campaign_id = $2`, orgID, campaignID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, id)
	}
	return out, mapErr(rows.Err())
}

// CampaignPreviewAudience counts who a launch would assign, without
// writing anything.
func (db *DB) CampaignPreviewAudience(ctx context.Context, orgID, id uuid.UUID) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx, `
		select count(*)
		from campaigns c
		left join lateral (
			select p.id from profiles p
			where p.org_id = c.org_id and p.status = 'active'
			  and p.role in ('learner','manager')
			  and (c.audience_type <> 'department' or p.department = c.audience_dept)
		) t on c.audience_type <> 'users'
		where c.org_id = $1 and c.id = $2`, orgID, id).Scan(&n)
	if err != nil {
		return 0, mapErr(err)
	}
	if n > 0 {
		return n, nil
	}
	// A "users" campaign resolves through campaign_audience.
	var m int
	if err := db.pool.QueryRow(ctx, `
		select count(*) from campaign_audience
		where org_id = $1 and campaign_id = $2`, orgID, id).Scan(&m); err != nil {
		return 0, mapErr(err)
	}
	return m, nil
}

func dedupeUUIDs(in []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(in))
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

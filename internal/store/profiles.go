package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

// ---------------------------------------------------------------------
// Organisations
// ---------------------------------------------------------------------

type CreateOrgParams struct {
	Name     string
	Slug     string
	Industry *string
	Country  *string
	Timezone string
	IsDemo   bool
}

func (db *DB) CreateOrg(ctx context.Context, p CreateOrgParams) (*domain.Organisation, error) {
	if p.Timezone == "" {
		p.Timezone = "UTC"
	}
	var o domain.Organisation
	err := db.pool.QueryRow(ctx, `
		insert into organisations (name, slug, industry, country, timezone, is_demo)
		values ($1, $2, $3, $4, $5, $6)
		returning id, name, slug, industry, country, timezone, is_demo, created_at`,
		p.Name, p.Slug, p.Industry, p.Country, p.Timezone, p.IsDemo,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Industry, &o.Country, &o.Timezone, &o.IsDemo, &o.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

func (db *DB) OrgByID(ctx context.Context, id uuid.UUID) (*domain.Organisation, error) {
	var o domain.Organisation
	err := db.pool.QueryRow(ctx, `
		select id, name, slug, industry, country, timezone, is_demo, created_at
		from organisations where id = $1`, id).
		Scan(&o.ID, &o.Name, &o.Slug, &o.Industry, &o.Country, &o.Timezone, &o.IsDemo, &o.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

// UpdateOrgParams carries only the fields an administrator may change.
// The slug is deliberately absent: it appears in shared certificate links
// and must stay stable once the organisation exists.
type UpdateOrgParams struct {
	Name     *string
	Industry *string
	Country  *string
	Timezone *string
}

func (db *DB) UpdateOrg(ctx context.Context, id uuid.UUID, p UpdateOrgParams) error {
	set := []string{"updated_at = now()"}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.Industry != nil {
		add("industry", *p.Industry)
	}
	if p.Country != nil {
		add("country", *p.Country)
	}
	if p.Timezone != nil {
		add("timezone", *p.Timezone)
	}
	tag, err := db.pool.Exec(ctx,
		`update organisations set `+strings.Join(set, ", ")+` where id = $1`, args...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) OrgBySlug(ctx context.Context, slug string) (*domain.Organisation, error) {
	var o domain.Organisation
	err := db.pool.QueryRow(ctx, `
		select id, name, slug, industry, country, timezone, is_demo, created_at
		from organisations where slug = $1`, slug).
		Scan(&o.ID, &o.Name, &o.Slug, &o.Industry, &o.Country, &o.Timezone, &o.IsDemo, &o.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

// ---------------------------------------------------------------------
// Profiles
// ---------------------------------------------------------------------

const profileCols = `
	id, auth_user_id, org_id, email, full_name, role, department, employee_number,
	job_title, phone, status, avatar_url, manager_id, must_reset, last_seen_at,
	created_at, updated_at`

func scanProfile(row interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.AuthUserID, &u.OrgID, &u.Email, &u.FullName, &u.Role,
		&u.Department, &u.EmployeeNumber, &u.JobTitle, &u.Phone, &u.Status,
		&u.AvatarURL, &u.ManagerID, &u.MustReset, &u.LastSeenAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

type CreateUserParams struct {
	OrgID          uuid.UUID
	AuthUserID     *uuid.UUID
	Email          string
	FullName       string
	Role           domain.Role
	Department     *string
	EmployeeNumber *string
	JobTitle       *string
	Phone          *string
	Status         domain.UserStatus
	AvatarURL      *string
	ManagerID      *uuid.UUID
	MustReset      bool
	// PasswordHash is a bcrypt digest, used only by AUTH_MODE=local.
	PasswordHash *string
}

func (db *DB) CreateUser(ctx context.Context, p CreateUserParams) (*domain.User, error) {
	if p.Status == "" {
		p.Status = domain.UserActive
	}
	return scanProfile(db.pool.QueryRow(ctx, `
		insert into profiles
			(org_id, auth_user_id, email, full_name, role, department, employee_number,
			 job_title, phone, status, avatar_url, manager_id, must_reset, password_hash)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		returning `+profileCols,
		p.OrgID, p.AuthUserID, p.Email, p.FullName, p.Role, p.Department,
		p.EmployeeNumber, p.JobTitle, p.Phone, p.Status, p.AvatarURL,
		p.ManagerID, p.MustReset, p.PasswordHash))
}

type UpdateUserParams struct {
	FullName       *string
	Department     *string
	EmployeeNumber *string
	JobTitle       *string
	Phone          *string
	Role           *domain.Role
	Status         *domain.UserStatus
	ManagerID      *uuid.UUID
	ClearManager   bool
	AuthUserID     *uuid.UUID
	MustReset      *bool
	AvatarURL      *string
}

func (db *DB) UpdateUser(ctx context.Context, orgID, id uuid.UUID, p UpdateUserParams) (*domain.User, error) {
	set := []string{"updated_at = now()"}
	args := []any{orgID, id}
	add := func(clause string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", clause, len(args)))
	}
	if p.FullName != nil {
		add("full_name", *p.FullName)
	}
	if p.Department != nil {
		add("department", *p.Department)
	}
	if p.EmployeeNumber != nil {
		add("employee_number", *p.EmployeeNumber)
	}
	if p.JobTitle != nil {
		add("job_title", *p.JobTitle)
	}
	if p.Phone != nil {
		add("phone", *p.Phone)
	}
	if p.Role != nil {
		add("role", *p.Role)
	}
	if p.Status != nil {
		add("status", *p.Status)
	}
	if p.ManagerID != nil {
		add("manager_id", *p.ManagerID)
	}
	if p.ClearManager {
		set = append(set, "manager_id = null")
	}
	if p.AuthUserID != nil {
		add("auth_user_id", *p.AuthUserID)
	}
	if p.MustReset != nil {
		add("must_reset", *p.MustReset)
	}
	if p.AvatarURL != nil {
		add("avatar_url", *p.AvatarURL)
	}

	q := `update profiles set ` + strings.Join(set, ", ") + ` where org_id = $1 and id = $2 returning ` + profileCols
	return scanProfile(db.pool.QueryRow(ctx, q, args...))
}

// UserByEmail is used by login before a user id is known.
func (db *DB) UserByEmail(ctx context.Context, orgID uuid.UUID, email string) (*domain.User, error) {
	return scanProfile(db.pool.QueryRow(ctx,
		`select `+profileCols+` from profiles where org_id = $1 and lower(email) = lower($2)`,
		orgID, strings.TrimSpace(email)))
}

func (db *DB) UserByAuthID(ctx context.Context, authUserID uuid.UUID) (*domain.User, error) {
	return scanProfile(db.pool.QueryRow(ctx,
		`select `+profileCols+` from profiles where auth_user_id = $1`, authUserID))
}

func (db *DB) UserByID(ctx context.Context, orgID, id uuid.UUID) (*domain.User, error) {
	return scanProfile(db.pool.QueryRow(ctx,
		`select `+profileCols+` from profiles where org_id = $1 and id = $2`, orgID, id))
}

// UserByAnyID resolves a profile from either the KaziWise id or the
// Supabase auth id. The middleware needs this because a verified token
// carries only the auth id when user_metadata is stale.
func (db *DB) UserByAnyID(ctx context.Context, orgID, id, authID uuid.UUID) (*domain.User, error) {
	return scanProfile(db.pool.QueryRow(ctx,
		`select `+profileCols+` from profiles
		 where org_id = $1 and (id = $2 or auth_user_id = $3)`, orgID, id, authID))
}

func (db *DB) DeleteUser(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := db.pool.Exec(ctx,
		`delete from profiles where org_id = $1 and id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) TouchUserSeen(ctx context.Context, id uuid.UUID) {
	_, _ = db.pool.Exec(ctx, `update profiles set last_seen_at = now() where id = $1`, id)
}

// UserListFilter narrows the employee table.
type UserListFilter struct {
	Search     string
	Department string
	Role       string
	Status     string
	ManagerID  *uuid.UUID
	// Unassigned matches employees with no manager set.
	Unassigned bool
	// WithoutCourseID excludes employees who already hold an assignment
	// for that course. It is what the "assign training" picker needs: the
	// point is to show only the people still to be assigned.
	WithoutCourseID *uuid.UUID
	Page            int
	PerPage         int
	Sort            string
	Order           string
}

const userListJoin = `
	select p.id, p.auth_user_id, p.org_id, p.email, p.full_name, p.role, p.department,
	       p.employee_number, p.job_title, p.phone, p.status, p.avatar_url, p.manager_id,
	       p.must_reset, p.last_seen_at, p.created_at, p.updated_at,
	       m.full_name,
	       coalesce(stats.assigned, 0)  as assigned_count,
	       coalesce(stats.completed, 0) as completed_count,
	       coalesce(stats.passed, 0)    as passed_count,
	       coalesce(stats.progress, 0)  as progress_percent
	from profiles p
	left join profiles m on m.id = p.manager_id
	left join lateral (
	    -- "Completed" means learning completed (completed_at) or passed.
	    -- assignment_status has no 'completed' member.
	    select count(*) filter (where true) as assigned,
	           count(*) filter (where a.completed_at is not null or a.status = 'passed') as completed,
	           count(*) filter (where a.status = 'passed') as passed,
	           coalesce(round(avg(a.progress_percent), 2), 0) as progress
	    from assignments a where a.learner_id = p.id
	) stats on true`

func (db *DB) ListUsers(ctx context.Context, orgID uuid.UUID, f UserListFilter) ([]domain.User, int, error) {
	b := orgScope(orgID)
	if f.Search != "" {
		like := "%" + f.Search + "%"
		// One placeholder reused three times, so the argument is appended
		// once rather than three times.
		b.add("(p.full_name ilike "+b.arg()+" or p.email ilike "+b.arg()+
			" or coalesce(p.employee_number,'') ilike "+b.arg()+")", like, like, like)
	}
	if f.Department != "" {
		b.add("p.department = "+b.arg(), f.Department)
	}
	if f.Role != "" {
		b.add("p.role = "+b.arg(), f.Role)
	}
	if f.Status != "" {
		b.add("p.status = "+b.arg(), f.Status)
	}
	if f.ManagerID != nil {
		b.add("p.manager_id = "+b.arg(), *f.ManagerID)
	}
	if f.Unassigned {
		b.add("p.manager_id is null")
	}
	if f.WithoutCourseID != nil {
		// A passed assignment still counts as assigned: the learner already
		// did this course, so offering it again would be misleading.
		b.add("not exists (select 1 from assignments a2 where a2.learner_id = p.id and a2.course_id = "+
			b.arg()+")", *f.WithoutCourseID)
	}

	// Rewrite the unqualified column names to the aliased table.
	where := strings.ReplaceAll(b.whereClause(), "org_id =", "p.org_id =")
	where = strings.ReplaceAll(where, "full_name ilike", "p.full_name ilike")
	where = strings.ReplaceAll(where, "email ilike", "p.email ilike")
	where = strings.ReplaceAll(where, "employee_number", "coalesce(p.employee_number,'')")

	from := strings.Replace(userListJoin, "\n\t", "\n\t", 1)
	base := from + where
	countSQL := `select count(*) from profiles p` + where

	sortCol := map[string]string{
		"":           "p.full_name",
		"name":       "p.full_name",
		"email":      "p.email",
		"role":       "p.role",
		"dept":       "p.department",
		"department": "p.department",
		"created":    "p.created_at",
		"status":     "p.status",
	}[f.Sort]
	if sortCol == "" {
		sortCol = "p.full_name"
	}
	order := "asc"
	if strings.EqualFold(f.Order, "desc") {
		order = "desc"
	}
	base += " order by " + sortCol + " " + order + ", p.id asc"

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.AuthUserID, &u.OrgID, &u.Email, &u.FullName, &u.Role,
			&u.Department, &u.EmployeeNumber, &u.JobTitle, &u.Phone, &u.Status,
			&u.AvatarURL, &u.ManagerID, &u.MustReset, &u.LastSeenAt, &u.CreatedAt, &u.UpdatedAt,
			&u.ManagerName, &u.AssignedCount, &u.CompletedCount, &u.PassedCount, &u.ProgressPct); err != nil {
			return nil, 0, mapErr(err)
		}
		out = append(out, u)
	}
	return out, total, mapErr(rows.Err())
}

// ---------------------------------------------------------------------
// Departments
// ---------------------------------------------------------------------

func (db *DB) ListDepartments(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := db.pool.Query(ctx, `
		select name from departments where org_id = $1 order by name`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, n)
	}
	return out, mapErr(rows.Err())
}

// EnsureDepartment adds a department if it is not already known, so CSV
// import of a new department name just works.
func (db *DB) EnsureDepartment(ctx context.Context, orgID uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	_, err := db.pool.Exec(ctx,
		`insert into departments (org_id, name) values ($1,$2) on conflict (org_id, name) do nothing`,
		orgID, name)
	return mapErr(err)
}

// ---------------------------------------------------------------------
// Team (manager view)
// ---------------------------------------------------------------------

// TeamMembers returns the direct reports of a manager with their current
// assignment state, which is what screen 08 / the manager report needs.
func (db *DB) TeamMembers(ctx context.Context, orgID, managerID uuid.UUID) ([]domain.TeamMember, error) {
	rows, err := db.pool.Query(ctx, `
		select p.id, p.auth_user_id, p.org_id, p.email, p.full_name, p.role, p.department,
		       p.employee_number, p.job_title, p.phone, p.status, p.avatar_url, p.manager_id,
		       p.must_reset, p.last_seen_at, p.created_at, p.updated_at,
		       mgr.full_name,
		       a.status::text, co.title, a.progress_percent, a.final_score,
		       a.due_date, a.last_activity_at
		from profiles p
		left join profiles mgr on mgr.id = p.manager_id
		left join lateral (
		    select a2.status, a2.progress_percent, a2.final_score, a2.due_date, a2.last_activity_at
		    from assignments a2
		    join campaigns c2 on c2.id = a2.campaign_id
		    join courses co2 on co2.id = a2.course_id
		    where a2.learner_id = p.id and c2.status = 'active'
		    order by a2.due_date asc nulls last, a2.assigned_at desc
		    limit 1
		) a on true
		left join courses co on co.id = (select course_id from assignments where learner_id = p.id limit 1)
		where p.org_id = $1 and p.manager_id = $2
		order by p.full_name`, orgID, managerID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []domain.TeamMember
	for rows.Next() {
		var tm domain.TeamMember
		var status *string
		if err := rows.Scan(&tm.ID, &tm.AuthUserID, &tm.OrgID, &tm.Email, &tm.FullName, &tm.Role,
			&tm.Department, &tm.EmployeeNumber, &tm.JobTitle, &tm.Phone, &tm.Status,
			&tm.AvatarURL, &tm.ManagerID, &tm.MustReset, &tm.LastSeenAt, &tm.CreatedAt, &tm.UpdatedAt,
			&tm.ManagerName, &status, &tm.CourseTitle, &tm.ProgressPct, &tm.FinalScore,
			&tm.DueDate, &tm.LastActivityAt); err != nil {
			return nil, mapErr(err)
		}
		if status != nil {
			tm.Status = domain.AssignmentStatus(*status)
		}
		out = append(out, tm)
	}
	return out, mapErr(rows.Err())
}

// CountTeam reports how many direct reports a manager has.
func (db *DB) CountTeam(ctx context.Context, orgID, managerID uuid.UUID) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx,
		`select count(*) from profiles where org_id = $1 and manager_id = $2`, orgID, managerID).Scan(&n)
	return n, mapErr(err)
}

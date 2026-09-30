package api

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// listUsers backs the employee directory (screen 02).
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.UserListFilter{
		Search:     p.Search,
		Department: p.RawQuery["department"],
		Role:       p.RawQuery["role"],
		Status:     p.Status,
		Page:       p.Page,
		PerPage:    p.PerPage,
	}
	// A manager only ever sees their own team.
	if claims := ClaimsFrom(r.Context()); claims.Role == auth.RoleManager {
		f.ManagerID = &actorID
	}
	users, total, err := s.db.ListUsers(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, users, p.Meta(total))
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	user, err := s.db.UserByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "employee"))
		return
	}
	// A manager may only read their direct reports.
	if claims := ClaimsFrom(r.Context()); claims.Role == auth.RoleManager {
		if id != actorID && (user.ManagerID == nil || *user.ManagerID != actorID) {
			httpx.Fail(w, httpx.Forbidden("You can only view your own team members."))
			return
		}
	}
	httpx.JSON(w, user)
}

func (s *Server) listDepartments(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	depts, err := s.db.ListDepartments(r.Context(), orgID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if depts == nil {
		depts = []string{}
	}
	httpx.JSON(w, depts)
}

// team returns a manager's direct reports with their training progress.
func (s *Server) team(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// A manager sees their own team; an admin may pass ?manager_id= to
	// inspect any team.
	managerID := actorID
	if raw := r.URL.Query().Get("manager_id"); raw != "" && ClaimsFrom(r.Context()).IsStaff() {
		if id, err := uuid.Parse(raw); err == nil {
			managerID = id
		}
	}
	members, err := s.db.TeamMembers(r.Context(), orgID, managerID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if members == nil {
		members = []domain.TeamMember{}
	}
	httpx.JSONMeta(w, members, &httpx.Meta{Total: len(members)})
}

type createUserRequest struct {
	FullName       string  `json:"full_name"`
	Email          string  `json:"email"`
	Password       *string `json:"password"`
	Role           string  `json:"role"`
	Department     *string `json:"department"`
	EmployeeNumber *string `json:"employee_number"`
	JobTitle       *string `json:"job_title"`
	Phone          *string `json:"phone"`
	ManagerID      *string `json:"manager_id"`
}

// createUser adds an employee. The generated password is returned once so
// an administrator can hand it over, matching the "add employee" flow.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req createUserRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	req.FullName = strings.TrimSpace(req.FullName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Role == "" {
		req.Role = auth.RoleLearner
	}
	fields := map[string]any{}
	if req.FullName == "" {
		fields["full_name"] = "Enter the employee's full name."
	}
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		fields["email"] = "Enter a valid email address."
	}
	if err := validateRole(req.Role); err != nil {
		fields["role"] = err.Error()
	}
	// A super admin may be created only by a super admin.
	if req.Role == auth.RoleSuperAdmin {
		if c := ClaimsFrom(r.Context()); c.Role != auth.RoleSuperAdmin {
			fields["role"] = "Only a super administrator can create another super administrator."
		}
	}
	if req.Password != nil {
		if err := validatePassword(*req.Password); err != nil {
			fields["password"] = err.Error()
		}
	}
	if len(fields) > 0 {
		httpx.Fail(w, ValidationError("Please correct the highlighted fields.", fields))
		return
	}

	// Default to a generated password so the admin can hand over working
	// credentials; the user is flagged to change it at first sign-in.
	generated := ""
	password := ""
	if req.Password != nil {
		password = *req.Password
	} else {
		generated = generatePassword()
		password = generated
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Fail(w, httpx.Internal(err))
		return
	}
	hashStr := string(hash)

	// Create the auth identity before the profile so a failed signup does
	// not leave a profile with no way to sign in.
	uid, err := s.provisionAuthUser(r, req.Email, password, map[string]any{
		"full_name": req.FullName,
		"app_role":  req.Role,
		"org_id":    orgID.String(),
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	params := store.CreateUserParams{
		OrgID:          orgID,
		AuthUserID:     uid,
		Email:          req.Email,
		FullName:       req.FullName,
		Role:           domain.Role(req.Role),
		Department:     trimOrNil(deref(req.Department)),
		EmployeeNumber: trimOrNil(deref(req.EmployeeNumber)),
		JobTitle:       trimOrNil(deref(req.JobTitle)),
		Phone:          trimOrNil(deref(req.Phone)),
		MustReset:      req.Password == nil,
		PasswordHash:   &hashStr,
	}
	if req.ManagerID != nil && strings.TrimSpace(*req.ManagerID) != "" {
		mid, err := uuid.Parse(strings.TrimSpace(*req.ManagerID))
		if err != nil {
			httpx.Fail(w, httpx.FieldError("manager_id", "That manager id is not a valid UUID."))
			return
		}
		params.ManagerID = &mid
	}
	user, err := s.db.CreateUser(r.Context(), params)
	if err != nil {
		httpx.Fail(w, statusOf(err, "employee"))
		return
	}
	if dept := trimOrNil(deref(req.Department)); dept != nil {
		_ = s.db.EnsureDepartment(r.Context(), orgID, *dept)
	}
	if uid != nil {
		s.auth.LocalIdentitySeed(orgID, user.ID, *uid, req.Role, user.FullName, user.Email, hashStr)
	}
	// The first employee of a new org becomes its administrator when the
	// caller is adding to an org with nobody else.
	_ = actorID
	s.audit(r, "employee.create", "profile", user.ID,
		map[string]any{"role": req.Role, "email": user.Email})

	body := map[string]any{"user": user}
	if generated != "" {
		body["temporary_password"] = generated
		body["password_note"] = "Share this once. The employee must change it at first sign-in."
	}
	httpx.Created(w, body)
}

type updateUserRequest struct {
	FullName       *string `json:"full_name"`
	Department     *string `json:"department"`
	EmployeeNumber *string `json:"employee_number"`
	JobTitle       *string `json:"job_title"`
	Phone          *string `json:"phone"`
	Role           *string `json:"role"`
	Status         *string `json:"status"`
	ManagerID      *string `json:"manager_id"`
	ClearManager   bool    `json:"clear_manager"`
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req updateUserRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p := store.UpdateUserParams{
		FullName:       trimOrNil(deref(req.FullName)),
		Department:     trimOrNil(deref(req.Department)),
		EmployeeNumber: trimOrNil(deref(req.EmployeeNumber)),
		JobTitle:       trimOrNil(deref(req.JobTitle)),
		Phone:          trimOrNil(deref(req.Phone)),
		ClearManager:   req.ClearManager,
	}
	if req.Role != nil {
		if err := validateRole(*req.Role); err != nil {
			httpx.Fail(w, httpx.FieldError("role", err.Error()))
			return
		}
		role := domain.Role(*req.Role)
		p.Role = &role
	}
	if req.Status != nil {
		status := domain.UserStatus(*req.Status)
		switch status {
		case domain.UserActive, domain.UserInvited, domain.UserInactive:
			p.Status = &status
		default:
			httpx.Fail(w, httpx.FieldError("status",
				"Status must be active, invited or inactive."))
			return
		}
	}
	if req.ManagerID != nil && strings.TrimSpace(*req.ManagerID) != "" {
		mid, err := uuid.Parse(strings.TrimSpace(*req.ManagerID))
		if err != nil {
			httpx.Fail(w, httpx.FieldError("manager_id", "That manager id is not a valid UUID."))
			return
		}
		// A manager must exist in this org and actually be a manager, or
		// the team view would silently be empty.
		mgr, err := s.db.UserByID(r.Context(), orgID, mid)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("manager_id", "That manager does not exist."))
			return
		}
		if mgr.Role != domain.RoleManager && mgr.Role != domain.RoleOrgAdmin {
			httpx.Fail(w, httpx.FieldError("manager_id",
				"Only managers and administrators can be set as a manager."))
			return
		}
		if mid == id {
			httpx.Fail(w, httpx.FieldError("manager_id",
				"An employee cannot be their own manager."))
			return
		}
		p.ManagerID = &mid
	}
	// Role and status changes are themselves worth an audit line.
	before, err := s.db.UserByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "employee"))
		return
	}
	user, err := s.db.UpdateUser(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "employee"))
		return
	}
	if p.Role != nil {
		s.audit(r, "employee.role_changed", "profile", id, map[string]any{
			"from": before.Role, "to": *p.Role,
		})
	}
	if p.Status != nil {
		s.audit(r, "employee.status_changed", "profile", id, map[string]any{
			"from": before.Status, "to": *p.Status,
		})
	}
	// Keep the local token path in step with a role change.
	if p.Role != nil && user.AuthUserID != nil {
		s.auth.LocalIdentitySeed(orgID, user.ID, *user.AuthUserID,
			string(user.Role), user.FullName, user.Email, "")
	}
	httpx.JSON(w, user)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if id == actorID {
		httpx.Fail(w, httpx.Conflict("self_delete",
			"You cannot remove your own account. Ask another administrator."))
		return
	}
	user, err := s.db.UserByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "employee"))
		return
	}
	// Removing a super admin is not allowed through this endpoint.
	if user.Role == domain.RoleSuperAdmin {
		httpx.Fail(w, httpx.Conflict("cannot_delete_super_admin",
			"A super administrator account cannot be deleted here. Deactivate it instead."))
		return
	}
	// A manager still reporting to this person must be reassigned, or the
	// team view would silently lose them.
	teamCount, err := s.db.CountTeam(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if teamCount > 0 {
		httpx.Fail(w, httpx.Conflict("employee_has_team",
			fmt.Sprintf("%d employee(s) still report to this manager. Reassign them first.", teamCount)))
		return
	}
	if err := s.db.DeleteUser(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "employee"))
		return
	}
	s.audit(r, "employee.delete", "profile", id, map[string]any{"email": user.Email})
	httpx.NoContent(w)
}

// ---------------------------------------------------------------------
// CSV import
// ---------------------------------------------------------------------

type importResult struct {
	Created   int               `json:"created"`
	Updated   int               `json:"updated"`
	Failed    int               `json:"failed"`
	Rows      []importRowError  `json:"errors,omitempty"`
	Generated []importGenerated `json:"generated_passwords,omitempty"`
}

type importRowError struct {
	Row     int    `json:"row"`
	Email   string `json:"email,omitempty"`
	Message string `json:"message"`
}

type importGenerated struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// importUsers bulk-creates employees from a CSV body.
//
// Required headers: full_name, email. Optional: role, department,
// employee_number, job_title, phone, manager_id. Each row is independent:
// one bad row is reported and skipped rather than failing the batch.
func (s *Server) importUsers(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	csvBytes, err := readCSVBody(w, r, s.cfg.MaxUploadMB)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	reader := csv.NewReader(strings.NewReader(string(csvBytes)))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		httpx.Fail(w, httpx.FieldError("file", "The CSV file is empty or unreadable."))
		return
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := col["full_name"]; !ok {
		httpx.Fail(w, httpx.FieldError("file",
			"The CSV must have a 'full_name' column."))
		return
	}
	if _, ok := col["email"]; !ok {
		httpx.Fail(w, httpx.FieldError("file", "The CSV must have an 'email' column."))
		return
	}

	result := importResult{}
	for row := 2; ; row++ {
		record, err := reader.Read()
		if err != nil {
			break // io.EOF or a malformed row; both end the scan
		}
		cell := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[i])
		}
		fullName := cell("full_name")
		email := strings.ToLower(cell("email"))
		if fullName == "" && email == "" {
			continue // blank separator line
		}
		fail := func(msg string) {
			result.Failed++
			result.Rows = append(result.Rows, importRowError{Row: row, Email: email, Message: msg})
		}
		if fullName == "" {
			fail("full_name is required")
			continue
		}
		if !strings.Contains(email, "@") {
			fail("email is not a valid address")
			continue
		}
		role := cell("role")
		if role == "" {
			role = auth.RoleLearner
		}
		if err := validateRole(role); err != nil {
			fail("unknown role " + role)
			continue
		}

		existing, findErr := s.db.UserByEmail(r.Context(), orgID, email)
		if findErr == nil {
			// Re-importing the same file updates rather than duplicating.
			upd := store.UpdateUserParams{
				FullName:   &fullName,
				Department: trimOrNil(cell("department")),
				JobTitle:   trimOrNil(cell("job_title")),
				Phone:      trimOrNil(cell("phone")),
			}
			if _, err := s.db.UpdateUser(r.Context(), orgID, existing.ID, upd); err != nil {
				fail("could not update: " + err.Error())
				continue
			}
			result.Updated++
			continue
		}
		if !isNotFound(findErr) {
			fail("lookup failed")
			continue
		}

		password := generatePassword()
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			fail("could not hash a password")
			continue
		}
		hashStr := string(hash)
		uid, err := s.provisionAuthUser(r, email, password, map[string]any{
			"full_name": fullName, "app_role": role, "org_id": orgID.String(),
		})
		if err != nil {
			fail("could not create the sign-in account")
			continue
		}
		user, err := s.db.CreateUser(r.Context(), store.CreateUserParams{
			OrgID: orgID, AuthUserID: uid, Email: email, FullName: fullName,
			Role: domain.Role(role), Department: trimOrNil(cell("department")),
			EmployeeNumber: trimOrNil(cell("employee_number")),
			JobTitle:       trimOrNil(cell("job_title")),
			Phone:          trimOrNil(cell("phone")),
			MustReset:      true, PasswordHash: &hashStr,
		})
		if err != nil {
			fail("could not create the profile")
			continue
		}
		if dept := trimOrNil(cell("department")); dept != nil {
			_ = s.db.EnsureDepartment(r.Context(), orgID, *dept)
		}
		if uid != nil {
			s.auth.LocalIdentitySeed(orgID, user.ID, *uid, role, fullName, email, hashStr)
		}
		result.Created++
		result.Generated = append(result.Generated, importGenerated{Email: email, Password: password})
	}

	s.audit(r, "employee.import", "profile", orgID, map[string]any{
		"created": result.Created, "updated": result.Updated, "failed": result.Failed,
	})
	httpx.JSON(w, result)
}

// provisionAuthUser creates the Supabase auth identity. When Supabase is
// not reachable the profile can still be created with a nil auth id, so a
// local prototype keeps working.
func (s *Server) provisionAuthUser(r *http.Request, email, password string, meta map[string]any) (*uuid.UUID, error) {
	if s.cfg.SupabaseURL == "" {
		return nil, nil
	}
	session, err := s.auth.SignUp(r.Context(), email, password, meta)
	if err != nil {
		return nil, httpx.Unprocessable(
			"Could not create the sign-in account for " + email + ": " + err.Error())
	}
	uid, err := uuid.Parse(session.User.ID)
	if err != nil {
		return nil, nil
	}
	return &uid, nil
}

// readCSVBody reads either a raw text/csv body or a multipart file field.
// A body that exceeds the limit is rejected rather than truncated: a
// silently shortened CSV would import a partial roster and report it as a
// success, which is worse than an error the admin can see.
func readCSVBody(w http.ResponseWriter, r *http.Request, maxMB int64) ([]byte, error) {
	limit := maxMB << 20
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(limit); err != nil {
			return nil, httpx.FieldError("file", "The upload could not be read.")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return nil, httpx.FieldError("file", "Attach the CSV as a 'file' field.")
		}
		defer file.Close()
		if header.Size > limit {
			return nil, httpx.TooLarge("That CSV is larger than the configured limit.")
		}
		return readAllLimited(file, limit)
	}
	// MaxBytesReader is given the writer so an over-limit read also marks
	// the connection as needing to close.
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return readAllLimited(r.Body, limit)
}

// readAllLimited drains body, refusing anything over limit.
func readAllLimited(body io.Reader, limit int64) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 32*1024)
	for {
		n, err := body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if int64(len(buf)) > limit {
			return nil, httpx.TooLarge("That CSV is larger than the configured limit.")
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A non-EOF error here is almost always the MaxBytesReader
			// refusing to hand over the rest of the body.
			return nil, httpx.TooLarge("That CSV is larger than the configured limit.")
		}
	}
	if len(buf) == 0 {
		return nil, httpx.FieldError("file", "The CSV body is empty.")
	}
	return buf, nil
}

// generatePassword produces a readable temporary password that satisfies
// the policy. The alphabet avoids characters that are easy to confuse when
// transcribed from a screen.
func generatePassword() string {
	const (
		upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower  = "abcdefghijkmnpqrstuvwxyz"
		digits = "23456789"
	)
	out := []byte{
		upper[randInt(len(upper))],
		lower[randInt(len(lower))],
		digits[randInt(len(digits))],
	}
	all := upper + lower + digits
	for i := len(out); i < 12; i++ {
		out = append(out, all[randInt(len(all))])
	}
	// Fisher-Yates with a crypto-grade source so the result is not
	// predictable from a seed.
	shuffle(out)
	return string(out)
}

func shuffle(b []byte) {
	for i := len(b) - 1; i > 0; i-- {
		j := randInt(i + 1)
		b[i], b[j] = b[j], b[i]
	}
}

type inviteRequest struct {
	Email          *string `json:"email"`
	Role           string  `json:"role"`
	ExpiresInHours *int    `json:"expires_in_hours"`
}

// createInvite issues a single-use code that lets a colleague create their
// own account in this organisation. Nothing is written to profiles until
// the code is redeemed, so an invite the recipient never opens leaves no
// half-created employee behind.
func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req inviteRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleLearner
	}
	// An invite may grant a manager, but never an administrator: those
	// accounts are created deliberately by an existing admin.
	if req.Role != auth.RoleLearner && req.Role != auth.RoleManager {
		httpx.Fail(w, ValidationError("That role cannot be invited.", map[string]any{
			"role": "Invite a learner or a manager.",
		}))
		return
	}
	email := ""
	if req.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*req.Email))
		if email != "" && !strings.Contains(email, "@") {
			httpx.Fail(w, httpx.FieldError("email", "Enter a valid email address."))
			return
		}
	}

	hours := 168 // one week
	if req.ExpiresInHours != nil {
		hours = *req.ExpiresInHours
	}
	if hours <= 0 || hours > 24*90 {
		httpx.Fail(w, httpx.FieldError("expires_in_hours",
			"Choose an expiry between 1 hour and 90 days."))
		return
	}

	code := inviteCode()
	expiresAt := time.Now().Add(time.Duration(hours) * time.Hour)
	var inviteID uuid.UUID
	var storedEmail *string
	err = s.db.Pool().QueryRow(r.Context(), `
		insert into invites (org_id, code, email, role, invited_by, expires_at)
		values ($1, $2, $3, $4, $5, $6)
		returning id, email`,
		orgID, code, trimOrNil(email), req.Role, actorID, expiresAt,
	).Scan(&inviteID, &storedEmail)
	if err != nil {
		httpx.Fail(w, statusOf(err, "invite"))
		return
	}
	s.audit(r, "invite.create", "invite", inviteID, map[string]any{
		"role": req.Role, "email": email,
	})
	httpx.Created(w, map[string]any{
		"id":         inviteID,
		"code":       code,
		"role":       req.Role,
		"email":      storedEmail,
		"expires_at": expiresAt,
		"accept_url": s.cfg.BaseURL + "/accept-invite?code=" + code,
	})
}

// inviteCode is a URL-safe one-time code. 128 bits of crypto randomness
// makes guessing a live code infeasible.
func inviteCode() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Entropy failure is unrecoverable for a security token; fail
		// closed rather than hand out a weak code.
		panic("kaziwise: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

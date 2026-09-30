package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/config"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// authRoutes are the unauthenticated entry points.
// authRoutes returns the unauthenticated part of the auth surface. They
// are mounted without the authenticate middleware, so no route here may
// trust a role taken from the request body.
func (s *Server) authRoutes() func(chi.Router) {
	return func(r chi.Router) {
		r.Post("/register", s.register)
		r.Post("/login", s.login)
		r.Post("/orgs/resolve", s.resolveOrg)
		r.Post("/accept-invite", s.acceptInvite)
		// A refresh is used after the access token expires, so this route
		// must not sit behind the authenticate middleware. The refreshed
		// session is verified on its own inside the handler.
		r.Post("/refresh", s.refresh)
		r.Post("/forgot-password", s.forgotPassword)
	}
}

// ---------------------------------------------------------------------
// Registration (screen 01)
// ---------------------------------------------------------------------

type registerRequest struct {
	FullName   string  `json:"full_name"`
	Email      string  `json:"email"`
	Password   string  `json:"password"`
	Role       string  `json:"role"`
	OrgName    *string `json:"org_name"`
	OrgSlug    *string `json:"org_slug"`
	InviteCode *string `json:"invite_code"`
	Department *string `json:"department"`
	EmployeeNo *string `json:"employee_number"`
	JobTitle   *string `json:"job_title"`
}

type authResponse struct {
	AccessToken        string               `json:"access_token"`
	RefreshToken       string               `json:"refresh_token,omitempty"`
	TokenType          string               `json:"token_type"`
	ExpiresAt          time.Time            `json:"expires_at"`
	ExpiresIn          int                  `json:"expires_in"`
	User               *domain.User         `json:"user"`
	Org                *domain.Organisation `json:"org,omitempty"`
	MustChangePassword bool                 `json:"must_change_password"`
}

// register creates a KaziWise profile plus a Supabase auth identity.
//
// The role is constrained by who is calling: only a signed-in
// administrator may mint another administrator, and a self-registration
// may never claim a privileged role. That check lives on the server
// because the client cannot be trusted to enforce it.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	user, org, signup, err := s.createAccount(r, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	resp, err := s.sessionFor(r, user, org, signup)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.Created(w, resp)
}

// acceptInvite redeems an invite and creates the account in one step. The
// invite supplies the tenant and the role, so a learner cannot escalate
// themselves by guessing a code.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(deref(req.InviteCode)) == "" {
		httpx.Fail(w, httpx.FieldError("invite_code", "An invite code is required."))
		return
	}
	user, org, signup, err := s.createAccount(r, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	resp, err := s.sessionFor(r, user, org, signup)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.Created(w, resp)
}

// createAccount is the shared registration path: validate, resolve the
// tenant, provision the auth identity, then write the profile. Both
// /register and /accept-invite go through here so the two entry points
// cannot drift apart.
func (s *Server) createAccount(r *http.Request, req registerRequest) (*domain.User, *domain.Organisation, *auth.Session, error) {
	req.FullName = strings.TrimSpace(req.FullName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Role == "" {
		req.Role = auth.RoleLearner
	}

	fields := map[string]any{}
	switch {
	case req.FullName == "":
		fields["full_name"] = "Enter your full name."
	case len([]rune(req.FullName)) < 2:
		fields["full_name"] = "Enter at least 2 characters."
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		fields["email"] = "Enter a valid email address."
	}
	if err := validatePassword(req.Password); err != nil {
		fields["password"] = err.Error()
	}
	if err := validateRole(req.Role); err != nil {
		fields["role"] = err.Error()
	}
	if len(fields) > 0 {
		return nil, nil, nil, ValidationError("Please correct the highlighted fields.", fields)
	}

	actor := ClaimsFrom(r.Context())
	org, createdOrg, err := s.resolveRegistrationOrg(r, &req, actor)
	if err != nil {
		return nil, nil, nil, err
	}
	// Whoever creates a tenant runs it. Without this a brand new tenant
	// could be left with no one able to add employees or launch training.
	if createdOrg {
		req.Role = auth.RoleOrgAdmin
	}
	// An invite fixes the role; ignore whatever the body claimed. The invite
	// is itself the authorisation, so a role granted this way does not also
	// need a staff actor. The claim below still has to succeed, which is what
	// stops two people racing on the same code.
	inviteCode := strings.TrimSpace(deref(req.InviteCode))
	grantedByInvite := false
	if inviteCode != "" {
		inviteRole, ok := s.inviteRole(r, inviteCode)
		if !ok {
			return nil, nil, nil, httpx.BadRequest("invite_invalid",
				"That invitation is not valid, has expired, or has already been used.")
		}
		// Defensive: the column is a user_role, but a hand-edited row should
		// not be able to grant something that is not a real role.
		if err := validateRole(inviteRole); err != nil {
			return nil, nil, nil, httpx.NewError(http.StatusInternalServerError, "invite_invalid",
				"That invitation cannot be used. Ask an administrator for a new one.")
		}
		if createdOrg {
			return nil, nil, nil, httpx.BadRequest("invite_invalid",
				"That invitation does not belong to a new organisation.")
		}
		req.Role = inviteRole
		grantedByInvite = true
	}
	if !createdOrg && !grantedByInvite {
		if err := s.assertRoleAllowed(actor, req.Role); err != nil {
			return nil, nil, nil, err
		}
	}

	if _, err := s.db.UserByEmail(r.Context(), org.ID, req.Email); err == nil {
		return nil, nil, nil, httpx.Conflict("email_taken",
			"An account with that email already exists. Sign in instead.")
	} else if !isNotFound(err) {
		return nil, nil, nil, err
	}

	// Claim the invite before writing anything. The update is conditional
	// on the code still being open, so two simultaneous redemptions cannot
	// both win: the loser sees zero rows affected and is rejected. If a
	// later step fails the claim is released, so a typo in the auth provider
	// does not burn the code.
	committed := false
	if inviteCode != "" {
		tag, err := s.db.Pool().Exec(r.Context(),
			`update invites set accepted_at = now()
			 where code = $1 and accepted_at is null
			   and (expires_at is null or expires_at > now())`, inviteCode)
		if err != nil {
			return nil, nil, nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, nil, nil, httpx.Conflict("invite_used",
				"That invite has already been used. Ask an administrator for a new one.")
		}
		defer func() {
			if committed {
				return
			}
			// Best effort: finalizeInvite already logs its own failures, and
			// the boot path is not the place to surface a cleanup error.
			_, _ = s.db.Pool().Exec(context.WithoutCancel(r.Context()),
				`update invites set accepted_at = null, accepted_by = null
				 where code = $1`, inviteCode)
		}()
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		return nil, nil, nil, err
	}

	meta := map[string]any{
		"full_name": req.FullName,
		"app_role":  req.Role,
		"org_id":    org.ID.String(),
	}
	// Under local auth there is no external identity to provision: the
	// profile hash is the credential and the token is minted from it.
	// Supabase mode provisions the credential first, because a profile
	// without a usable sign-in identity would be an account nobody can
	// reach.
	var session *auth.Session
	var supabaseUID uuid.UUID
	if s.cfg.AuthMode == config.AuthLocal && s.cfg.AllowLocalAuth {
		supabaseUID = uuid.New()
	} else {
		session, err = s.auth.SignUp(r.Context(), req.Email, req.Password, meta)
		if err != nil {
			s.log.Warn("supabase signup failed", "error", err)
			return nil, nil, nil, httpx.Unprocessable(
				"Could not create the sign-in account. Check the email address or try again.")
		}
		supabaseUID, err = uuid.Parse(session.User.ID)
		if err != nil {
			// A Supabase project without UUID ids is not something this API
			// can key a profile on, so fail rather than store a broken link.
			return nil, nil, nil, httpx.Internal(errors.New("supabase returned a non-uuid user id"))
		}
	}

	user, err := s.db.CreateUser(r.Context(), store.CreateUserParams{
		OrgID:          org.ID,
		AuthUserID:     &supabaseUID,
		Email:          req.Email,
		FullName:       req.FullName,
		Role:           domain.Role(req.Role),
		Department:     trimOrNil(deref(req.Department)),
		EmployeeNumber: trimOrNil(deref(req.EmployeeNo)),
		JobTitle:       trimOrNil(deref(req.JobTitle)),
		PasswordHash:   &hash,
	})
	if err != nil {
		return nil, nil, nil, statusOf(err, "profile")
	}
	if dept := trimOrNil(deref(req.Department)); dept != nil {
		_ = s.db.EnsureDepartment(r.Context(), org.ID, *dept)
	}
	// Keep the local token path in step so AUTH_MODE=local works straight
	// after sign-up.
	s.auth.LocalIdentitySeed(org.ID, user.ID, supabaseUID, req.Role, user.FullName, user.Email, hash)

	if inviteCode != "" {
		// Record who redeemed the invite. The row is already marked
		// accepted, so this is the only remaining bookkeeping; a failure
		// here does not undo the account.
		if _, err := s.db.Pool().Exec(r.Context(),
			`update invites set accepted_by = $2 where code = $1`, inviteCode, user.ID); err != nil {
			s.log.Warn("could not record the invite redeemer", "invite", inviteCode, "error", err)
		}
		committed = true
	}
	s.auditSystem(r, "auth.register", "profile", user.ID,
		map[string]any{"role": req.Role, "org_id": org.ID})
	return user, org, session, nil
}

// assertRoleAllowed refuses privileged self-service. A manager or org
// admin can only be created by someone who is already staff.
func (s *Server) assertRoleAllowed(actor *auth.Claims, role string) error {
	switch role {
	case auth.RoleOrgAdmin, auth.RoleManager:
		if actor == nil || !actor.IsStaff() {
			return httpx.NewError(http.StatusForbidden, "privileged_role",
				"Only an administrator can create a manager or administrator account.")
		}
	}
	return nil
}

// inviteRole reads the role an invite grants, without consuming it.
func (s *Server) inviteRole(r *http.Request, code string) (string, bool) {
	var orgID uuid.UUID
	var role string
	err := s.db.Pool().QueryRow(r.Context(),
		`select org_id, role::text from invites
		 where code = $1 and accepted_at is null
		   and (expires_at is null or expires_at > now())`, code).Scan(&orgID, &role)
	if err != nil {
		return "", false
	}
	switch role {
	case auth.RoleLearner, auth.RoleManager:
		return role, true
	default:
		// An invite never grants an administrative role.
		return auth.RoleLearner, true
	}
}

// resolveRegistrationOrg works out which organisation a new profile joins:
// the invite's tenant, the caller's own tenant, a code, or a brand new one.
// The second result is true only when a new tenant was created, which the
// caller uses to make the founder its administrator.
func (s *Server) resolveRegistrationOrg(r *http.Request, req *registerRequest, actor *auth.Claims) (*domain.Organisation, bool, error) {
	if req.InviteCode != nil {
		if code := strings.TrimSpace(*req.InviteCode); code != "" {
			var orgID uuid.UUID
			if err := s.db.Pool().QueryRow(r.Context(),
				`select org_id from invites where code = $1 and accepted_at is null
				 and (expires_at is null or expires_at > now())`, code).Scan(&orgID); err != nil {
				return nil, false, httpx.FieldError("invite_code",
					"That invite code is invalid or has expired.")
			}
			org, err := s.db.OrgByID(r.Context(), orgID)
			return org, false, err
		}
	}
	// An administrator adding an employee joins their own tenant.
	if actor != nil && actor.OrgID != uuid.Nil {
		org, err := s.db.OrgByID(r.Context(), actor.OrgID)
		return org, false, err
	}
	if slug := strings.TrimSpace(deref(req.OrgSlug)); slug != "" {
		if _, err := s.db.OrgBySlug(r.Context(), slug); err == nil {
			// Knowing an organisation's code is not a credential. Without an
			// invite, or a signed-in administrator creating the account from
			// inside that tenant, this would let anyone who guesses a slug
			// drop themselves into someone else's roster.
			return nil, false, httpx.NewError(http.StatusForbidden, "invite_required",
				"An invitation is required to join this organisation.")
		} else if !isNotFound(err) {
			return nil, false, err
		}
		return nil, false, ValidationError("Unknown organisation.", map[string]any{
			"org_slug": "No organisation uses that code. Check the invite you were sent.",
		})
	}

	// Creating a tenant is only meaningful for the person who will run it,
	// so the first registrant becomes its org admin.
	name := strings.TrimSpace(deref(req.OrgName))
	if name == "" {
		name = req.FullName + "'s Organisation"
	}
	slug := slugOrDefault(name)
	org, err := s.db.CreateOrg(r.Context(), store.CreateOrgParams{Name: name, Slug: slug})
	if err != nil {
		if isConflict(err) {
			// The slug is taken: the same organisation is being set up
			// twice, so join it rather than failing. This is not a new
			// tenant, so the founder rule does not apply.
			existing, lookupErr := s.db.OrgBySlug(r.Context(), slug)
			return existing, false, lookupErr
		}
		return nil, false, err
	}
	return org, true, nil
}

// ---------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// OrgSlug disambiguates when the same address exists in more than one
	// organisation. POST /v1/auth/orgs/resolve lists the candidates.
	OrgSlug string `json:"org_slug"`
}

// login authenticates an existing user. Under AUTH_MODE=supabase the
// browser normally obtains its token from supabase-js and this endpoint
// is redundant; under AUTH_MODE=local it is how the prototype gets one.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		httpx.Fail(w, ValidationError("Email and password are required.", map[string]any{
			"email": "Enter your email address and password.",
		}))
		return
	}

	org, user, err := s.findUserByEmail(r, email, strings.TrimSpace(req.OrgSlug))
	if err != nil {
		var ambiguous *ambiguousOrgError
		if errors.As(err, &ambiguous) {
			// Picking the oldest profile silently would send this person
			// to the wrong tenant, so the client is asked to choose. The
			// candidates ride along in fields so the sign-in form can offer
			// them without a second request.
			e := httpx.NewError(http.StatusConflict, "org_selection_required",
				"This email address exists in more than one organisation. Enter your organisation code.")
			e.Fields = map[string]any{
				"org_slug":      "required when the address is in several organisations",
				"organisations": ambiguous.slugs,
			}
			httpx.Fail(w, e)
			return
		}
		if isNotFound(err) {
			// The same message for an unknown address and a wrong
			// password, so the endpoint cannot enumerate accounts.
			httpx.Fail(w, httpx.Unauthorized("Email or password is incorrect."))
			return
		}
		httpx.Fail(w, err)
		return
	}
	if user.Status == domain.UserInactive {
		httpx.Fail(w, httpx.NewError(http.StatusForbidden, "account_inactive",
			"Your account is not active. Contact an administrator."))
		return
	}

	if s.cfg.AuthMode == config.AuthSupabase {
		// Production path: Supabase is the authority on credentials.
		if s.cfg.SupabaseURL == "" {
			httpx.Fail(w, httpx.NewError(http.StatusServiceUnavailable, "auth_unconfigured",
				"Sign-in is not configured on this server."))
			return
		}
		session, err := s.auth.PasswordSignIn(r.Context(), email, req.Password)
		if err != nil {
			s.log.Debug("supabase sign-in rejected", "error", err)
			httpx.Fail(w, httpx.Unauthorized("Email or password is incorrect."))
			return
		}
		resp, err := s.buildAuthResponse(r, user, org, session)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		s.auditSystem(r, "auth.login", "profile", user.ID, nil)
		httpx.JSON(w, resp)
		return
	}

	// Local mode: the password is checked against the seeded bcrypt hash.
	ok, known := s.verifyPassword(r, user, email, req.Password)
	if !known || !ok {
		httpx.Fail(w, httpx.Unauthorized("Email or password is incorrect."))
		return
	}
	if !s.cfg.AllowLocalAuth {
		httpx.Fail(w, httpx.NewError(http.StatusServiceUnavailable, "login_disabled",
			"Local sign-in is disabled. Set ALLOW_LOCAL_AUTH=true for the prototype."))
		return
	}
	token, exp, err := s.mintLocalToken(r, user)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.auditSystem(r, "auth.login", "profile", user.ID, nil)
	httpx.JSON(w, authResponse{
		AccessToken: token, TokenType: "Bearer", ExpiresAt: exp,
		ExpiresIn:          int(time.Until(exp).Seconds()),
		User:               user,
		Org:                org,
		MustChangePassword: user.MustReset,
	})
}

// sessionFor is the shared post-registration response builder. The account
// was just created, so local mode mints its token directly.
func (s *Server) sessionFor(r *http.Request, user *domain.User, org *domain.Organisation, signup *auth.Session) (*authResponse, error) {
	if s.cfg.AuthMode == config.AuthLocal && s.cfg.AllowLocalAuth {
		token, exp, err := s.mintLocalToken(r, user)
		if err != nil {
			return nil, err
		}
		return &authResponse{
			AccessToken: token, TokenType: "Bearer", ExpiresAt: exp,
			ExpiresIn: int(time.Until(exp).Seconds()),
			User:      user, Org: org, MustChangePassword: user.MustReset,
		}, nil
	}
	// Supabase mode: reuse the session created by sign-up. Re-authenticating
	// with an empty password never works and only adds a failed login to
	// the audit trail, so it is not attempted.
	if signup == nil {
		return &authResponse{
			TokenType: "Bearer", User: user, Org: org,
			MustChangePassword: user.MustReset,
		}, nil
	}
	return s.buildAuthResponse(r, user, org, signup)
}

// verifyPassword checks a password against the in-memory local identity,
// falling back to the hash stored on the profile so a restart does not lock
// anyone out.
func (s *Server) verifyPassword(r *http.Request, user *domain.User, email, password string) (ok, known bool) {
	if s.cfg.AllowLocalAuth {
		if good, found := s.auth.VerifyLocalPassword(email, password); found {
			return good, true
		}
	}
	var hash string
	if err := s.db.Pool().QueryRow(r.Context(),
		`select coalesce(password_hash,'') from profiles where id = $1`, user.ID).Scan(&hash); err != nil {
		return false, false
	}
	if hash == "" {
		// A Supabase-only account has no local hash: this path cannot
		// authenticate it, but the account is not unknown.
		return false, true
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, true
}

// mintLocalToken issues a self-signed token, re-seeding the identity from
// the profile when the in-memory copy is missing.
func (s *Server) mintLocalToken(r *http.Request, user *domain.User) (string, time.Time, error) {
	ident, ok := s.auth.LookupLocalIdentity(user.Email)
	if !ok {
		var hash string
		if err := s.db.Pool().QueryRow(r.Context(),
			`select coalesce(password_hash,'') from profiles where id = $1`, user.ID).Scan(&hash); err != nil {
			return "", time.Time{}, httpx.Internal(err)
		}
		uid := user.ID
		if user.AuthUserID != nil {
			uid = *user.AuthUserID
		}
		s.auth.LocalIdentitySeed(user.OrgID, user.ID, uid,
			string(user.Role), user.FullName, user.Email, hash)
		if ident, ok = s.auth.LookupLocalIdentity(user.Email); !ok {
			return "", time.Time{}, httpx.Internal(errNoLocalIdentity)
		}
	}
	token, exp, err := s.auth.Mint(ident, s.cfg.AccessTokenTTL)
	if err != nil {
		return "", time.Time{}, httpx.NewError(http.StatusServiceUnavailable,
			"local_auth_disabled", "Local sign-in is not enabled on this server.")
	}
	return token, exp, nil
}

var errNoLocalIdentity = errors.New("local identity could not be prepared")

// buildAuthResponse assembles the token payload from a Supabase session.
func (s *Server) buildAuthResponse(r *http.Request, user *domain.User, org *domain.Organisation, session *auth.Session) (*authResponse, error) {
	if session == nil {
		return nil, httpx.Internal(errNoSession)
	}
	expires := time.Now().Add(s.cfg.AccessTokenTTL)
	if session.ExpiresIn > 0 {
		expires = time.Now().Add(time.Duration(session.ExpiresIn) * time.Second)
	}
	// Supabase's app_metadata can be stale. Under local mode, re-mint so
	// the claims the API authorises on match the database.
	if s.cfg.AuthMode == config.AuthLocal && s.cfg.AllowLocalAuth {
		if token, exp, err := s.mintLocalToken(r, user); err == nil {
			return &authResponse{
				AccessToken: token, RefreshToken: session.RefreshToken,
				TokenType: "Bearer", ExpiresAt: exp,
				ExpiresIn: int(time.Until(exp).Seconds()),
				User:      user, Org: org, MustChangePassword: user.MustReset,
			}, nil
		}
	}
	return &authResponse{
		AccessToken: session.AccessToken, RefreshToken: session.RefreshToken,
		TokenType: "Bearer", ExpiresAt: expires,
		ExpiresIn: int(time.Until(expires).Seconds()),
		User:      user, Org: org, MustChangePassword: user.MustReset,
	}, nil
}

var errNoSession = errors.New("auth session missing")

// findUserByEmail locates the org and profile for an email address.
// ambiguousOrgError reports that one address has profiles in several
// organisations, so the caller must say which tenant they want.
type ambiguousOrgError struct {
	slugs []string
}

func (e *ambiguousOrgError) Error() string {
	return "email exists in multiple organisations: " + strings.Join(e.slugs, ", ")
}

// findUserByEmail resolves the tenant and profile for a sign-in. When the
// address exists in more than one organisation and no slug was supplied,
// it reports the candidates instead of guessing.
func (s *Server) findUserByEmail(r *http.Request, email, orgSlug string) (*domain.Organisation, *domain.User, error) {
	if orgSlug != "" {
		org, err := s.db.OrgBySlug(r.Context(), orgSlug)
		if err != nil {
			return nil, nil, err
		}
		user, err := s.db.UserByEmail(r.Context(), org.ID, email)
		if err != nil {
			return nil, nil, err
		}
		return org, user, nil
	}

	rows, err := s.db.Pool().Query(r.Context(), `
		select o.slug from organisations o
		  join profiles p on p.org_id = o.id
		 where lower(p.email) = $1
		 order by p.created_at asc`, email)
	if err != nil {
		return nil, nil, err
	}
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return nil, nil, err
		}
		slugs = append(slugs, slug)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	switch len(slugs) {
	case 0:
		return nil, nil, store.ErrNotFound
	case 1:
		org, err := s.db.OrgBySlug(r.Context(), slugs[0])
		if err != nil {
			return nil, nil, err
		}
		user, err := s.db.UserByEmail(r.Context(), org.ID, email)
		if err != nil {
			return nil, nil, err
		}
		return org, user, nil
	default:
		return nil, nil, &ambiguousOrgError{slugs: slugs}
	}
}

// ---------------------------------------------------------------------
// Session management
// ---------------------------------------------------------------------

// me returns the signed-in profile with the permissions the front end uses
// to decide which screens to show.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	orgID, userID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	user, err := s.db.UserByID(r.Context(), orgID, userID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "profile"))
		return
	}
	org, err := s.db.OrgByID(r.Context(), orgID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	c := ClaimsFrom(r.Context())
	httpx.JSON(w, map[string]any{
		"user": user,
		"org":  org,
		"permissions": map[string]bool{
			"manage_employees":   c.IsStaff(),
			"manage_courses":     c.IsAdmin(),
			"manage_campaigns":   c.IsAdmin(),
			"view_reports":       c.IsStaff(),
			"grade_assessments":  c.IsStaff(),
			"issue_certificates": c.IsStaff(),
			"take_training":      c.Role == auth.RoleLearner,
		},
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	// Tokens are stateless, so the client discards them. The audit entry
	// is the part the server can still record.
	s.audit(r, "auth.logout", "profile", mustUUIDOrZero(r), nil)
	httpx.NoContent(w)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		httpx.Fail(w, httpx.FieldError("refresh_token", "A refresh token is required."))
		return
	}
	sess, err := s.auth.RefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		httpx.Fail(w, httpx.Unauthorized("Your session has expired. Please sign in again."))
		return
	}
	// This route is public, so the caller cannot be trusted from the
	// request context. The refreshed access token is verified instead, and
	// the profile is resolved from those claims.
	claims, err := s.auth.Verify(r.Context(), sess.AccessToken)
	if err != nil || claims.OrgID == uuid.Nil {
		httpx.Fail(w, httpx.Unauthorized("Your session has expired. Please sign in again."))
		return
	}
	var user *domain.User
	switch {
	case claims.UserID != uuid.Nil:
		user, err = s.db.UserByID(r.Context(), claims.OrgID, claims.UserID)
	case claims.AuthUserID != uuid.Nil:
		user, err = s.db.UserByAuthID(r.Context(), claims.AuthUserID)
	default:
		user, err = s.db.UserByEmail(r.Context(), claims.OrgID, claims.Email)
	}
	if err != nil {
		if isNotFound(err) {
			httpx.Fail(w, httpx.NewError(http.StatusForbidden, "no_profile",
				"Your account is not set up in this organisation."))
			return
		}
		httpx.Fail(w, err)
		return
	}
	// A refresh must not be able to move the caller to another tenant.
	if user.OrgID != claims.OrgID {
		httpx.Fail(w, httpx.Forbidden("Your session belongs to a different organisation."))
		return
	}
	if user.Status == domain.UserInactive {
		httpx.Fail(w, httpx.NewError(http.StatusForbidden, "account_inactive",
			"Your account is not active. Contact an administrator."))
		return
	}
	org, _ := s.db.OrgByID(r.Context(), user.OrgID)
	resp, err := s.buildAuthResponse(r, user, org, sess)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, resp)
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		httpx.Fail(w, httpx.FieldError("email", "Enter your email address."))
		return
	}
	// Always report success so the endpoint cannot be used to discover
	// which addresses have accounts. The mail call is detached because a
	// failure must not change the response.
	email, base := email, s.cfg.BaseURL
	go func() {
		// Detached from the request: the response is already decided.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.auth.ForgotPassword(ctx, email, base+"/reset-password")
	}()
	httpx.JSON(w, map[string]any{
		"message": "If that address has an account, a reset link is on its way.",
	})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	orgID, userID, _ := orgAndActor(r)
	if err := validatePassword(req.NewPassword); err != nil {
		httpx.Fail(w, ValidationError("Choose a stronger password.", map[string]any{
			"new_password": err.Error(),
		}))
		return
	}
	user, err := s.db.UserByID(r.Context(), orgID, userID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "profile"))
		return
	}
	// The current password is required so a borrowed session cannot take
	// over the account. Accounts with no stored hash (Supabase-only) skip
	// this check, since the refresh flow covers them.
	var hash string
	_ = s.db.Pool().QueryRow(r.Context(),
		`select coalesce(password_hash,'') from profiles where id = $1`, userID).Scan(&hash)
	if hash != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)); err != nil {
			httpx.Fail(w, httpx.FieldError("current_password", "Your current password is incorrect."))
			return
		}
	}
	newHash, err := hashPassword(req.NewPassword)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if _, err := s.db.Pool().Exec(r.Context(),
		`update profiles set password_hash = $1, must_reset = false, updated_at = now()
		 where id = $2`, newHash, userID); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Both credential stores must agree, or the next sign-in differs
	// depending on which path the client takes.
	if s.cfg.AllowLocalAuth {
		uid := userID
		if user.AuthUserID != nil {
			uid = *user.AuthUserID
		}
		s.auth.LocalIdentitySeed(orgID, userID, uid,
			string(user.Role), user.FullName, user.Email, newHash)
	}
	if s.cfg.SupabaseURL != "" && user.AuthUserID != nil {
		_ = s.auth.AdminUpdateUser(r.Context(), user.AuthUserID.String(), req.NewPassword, nil)
	}
	s.audit(r, "auth.password_changed", "profile", userID, nil)
	httpx.JSON(w, map[string]any{"message": "Your password has been updated."})
}

// ---------------------------------------------------------------------
// Organisation lookup
// ---------------------------------------------------------------------

// resolveOrg turns an org code into its public details so the sign-up form
// can show which organisation it is joining.
func (s *Server) resolveOrg(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug string `json:"slug"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(req.Slug) == "" {
		httpx.Fail(w, httpx.FieldError("slug", "Enter your organisation code."))
		return
	}
	org, err := s.db.OrgBySlug(r.Context(), strings.TrimSpace(req.Slug))
	if err != nil {
		if isNotFound(err) {
			httpx.Fail(w, httpx.NewError(http.StatusNotFound, "org_not_found",
				"No organisation uses that code."))
			return
		}
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, map[string]any{"id": org.ID, "name": org.Name, "slug": org.Slug})
}

// ---------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------

// validatePassword enforces the platform password policy. The rules are
// duplicated in the front end; the server check is the authoritative one.
func validatePassword(p string) error {
	if len(p) < 8 {
		return errPasswordTooShort
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range p {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return errPasswordWeak
	}
	return nil
}

type passwordError string

func (e passwordError) Error() string { return string(e) }

const (
	errPasswordTooShort = passwordError("Use at least 8 characters.")
	errPasswordWeak     = passwordError("Include an uppercase letter, a lowercase letter and a number.")
)

func hashPassword(p string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return "", httpx.Internal(err)
	}
	return string(b), nil
}

func validateRole(r string) error {
	switch r {
	case auth.RoleLearner, auth.RoleManager, auth.RoleOrgAdmin, auth.RoleSuperAdmin:
		return nil
	default:
		return passwordError("Choose learner, manager or administrator.")
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mustUUIDOrZero(r *http.Request) uuid.UUID {
	if c := ClaimsFrom(r.Context()); c != nil {
		return c.UserID
	}
	return uuid.Nil
}

// slugOrDefault turns a name into a URL-safe organisation code.
func slugOrDefault(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "org"
	}
	return slug
}

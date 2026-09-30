package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/config"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// seedAuthUsers gives every profile in the demo organisation a working
// credential. The SQL seed writes the profiles but deliberately leaves the
// credential columns alone; this step is what makes the accounts usable.
//
// It is safe to run more than once: an address that already has a Supabase
// identity is linked to that identity rather than a second one, and the
// local identity store is idempotent.
func seedAuthUsers(ctx context.Context, db *store.DB, cfg *config.Config, authSvc *auth.Service, log *slog.Logger) error {
	org, err := db.OrgBySlug(ctx, cfg.SeedOrgSlug)
	if err != nil {
		return fmt.Errorf("demo organisation %q: %w", cfg.SeedOrgSlug, err)
	}
	users, _, err := db.ListUsers(ctx, org.ID, store.UserListFilter{PerPage: 500})
	if err != nil {
		return fmt.Errorf("list demo profiles: %w", err)
	}
	if len(users) == 0 {
		log.Warn("no demo profiles to seed", "org", cfg.SeedOrgSlug)
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.SeedPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	hashStr := string(hash)

	seeded := 0
	for i := range users {
		u := &users[i]
		uid, linked, err := linkAuthIdentity(ctx, authSvc, cfg, u, org.ID, log)
		if err != nil {
			return err
		}
		if !linked {
			// The profile has no usable auth identity, so it cannot sign in.
			// Skipping leaves the column null rather than storing an id that
			// matches no Supabase user, which would make every later request
			// fail as an unrecognised account.
			continue
		}

		if _, err := db.Pool().Exec(ctx, `
			update profiles
			   set password_hash = $2,
			       auth_user_id  = $3,
			       must_reset    = false,
			       updated_at    = now()
			 where id = $1 and org_id = $4`,
			u.ID, hashStr, uid, org.ID); err != nil {
			return fmt.Errorf("seed credentials for %s: %w", u.Email, err)
		}
		authSvc.LocalIdentitySeed(org.ID, u.ID, uid,
			string(u.Role), u.FullName, u.Email, hashStr)
		seeded++
	}
	// The shared demo password is deliberately not written to the log.
	// Boot output is routinely shipped to aggregators and CI, and a
	// credential that appears there outlives the demo it unlocks. The
	// operator already has it in their environment.
	log.Info("seeded demo credentials",
		"org", cfg.SeedOrgSlug, "accounts", seeded,
		"password_source", "SEED_PASSWORD")
	return nil
}

// linkAuthIdentity resolves the Supabase auth user that owns a demo profile
// and reports its id. The id is only ever a real one: it comes from Supabase
// or from a profile that has no external identity yet, never from a generated
// placeholder. A fabricated id is worse than a null column — the account
// signs in, then every authenticated request is rejected because the token's
// subject matches no profile.
func linkAuthIdentity(ctx context.Context, authSvc *auth.Service, cfg *config.Config,
	u *domain.User, orgID uuid.UUID, log *slog.Logger) (uuid.UUID, bool, error) {
	// Under local auth there is no external identity to provision: the
	// profile hash is the credential. The profile's own id stands in for the
	// auth id, which keeps the local token verifiable.
	if cfg.AuthMode != config.AuthSupabase {
		if u.AuthUserID != nil {
			return *u.AuthUserID, true, nil
		}
		return u.ID, true, nil
	}

	meta := map[string]any{
		"full_name": u.FullName,
		"app_role":  string(u.Role),
		"org_id":    orgID.String(),
	}
	au, err := authSvc.AdminCreateUser(ctx, u.Email, cfg.SeedPassword, meta)
	if err != nil {
		var authErr *auth.AuthError
		// The address is already registered: this is a re-seed, so link to
		// the identity that already holds the sign-in rather than failing.
		if errors.As(err, &authErr) && authErr.IsDuplicateEmail() {
			if au, err = authSvc.AdminFindUserByEmail(ctx, u.Email); err == nil {
				// An existing identity may carry a password or metadata from
				// an earlier seed, so put the demo credential and claims back.
				if err := authSvc.AdminUpdateUser(ctx, au.ID, cfg.SeedPassword, meta); err != nil {
					log.Warn("could not refresh the supabase identity", "email", u.Email, "error", err)
				}
			}
		}
		if err != nil {
			log.Warn("could not link the supabase identity; this account cannot sign in",
				"email", u.Email, "error", err)
			return uuid.Nil, false, nil
		}
	}
	uid, err := au.UUID()
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("supabase identity for %s: %w", u.Email, err)
	}
	return uid, true, nil
}

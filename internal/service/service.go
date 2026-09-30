// Package service holds the KaziWise business rules. Handlers stay thin:
// they decode, authorise and delegate here, and the rules live in one
// place that both the API and the tests can rely on.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// Service aggregates the dependencies every rule set needs.
type Service struct {
	DB  *store.DB
	Log *slog.Logger
}

// New builds the service container.
func New(db *store.DB, log *slog.Logger) *Service {
	return &Service{DB: db, Log: log}
}

// ---------------------------------------------------------------------
// Shared validation
// ---------------------------------------------------------------------

// ValidationError carries per-field messages to the front end.
func ValidationError(msg string, fields map[string]any) error {
	return httpx.Unprocessable(msg).WithFields(fields)
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
func isConflict(err error) bool { return errors.Is(err, store.ErrConflict) }

// Audit records an action in the immutable trail.
func (s *Service) Audit(ctx context.Context, p store.AuditParams) { s.DB.Audit(ctx, p) }

// ---------------------------------------------------------------------
// Certificate codes
// ---------------------------------------------------------------------

// codeAlphabet omits 0/O/1/I/L so a printed verification code cannot be
// mistyped. Crockford-style base32 keeps it case-insensitive on read.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// newVerificationCode produces a public, human-transcribable code. The
// database unique constraint is the authority; this only makes a
// collision unlikely enough to retry rather than fail.
func newVerificationCode() string {
	const groups, groupLen = 3, 4
	b := make([]byte, 0, groups*groupLen+groups-1)
	for g := 0; g < groups; g++ {
		if g > 0 {
			b = append(b, '-')
		}
		for i := 0; i < groupLen; i++ {
			b = append(b, codeAlphabet[rand.Intn(len(codeAlphabet))])
		}
	}
	return string(b)
}

// issueCertificateFor creates the certificate for a passing attempt,
// reusing one that already exists so a retake never doubles up. completedAt
// is the learner's completion moment, which is not the same as the moment
// the certificate is issued.
func (s *Service) issueCertificateFor(ctx context.Context, orgID uuid.UUID, ac *store.AssignmentContext, attemptID *uuid.UUID, score float64, completedAt time.Time) (*domain.Certificate, error) {
	if attemptID != nil {
		if existing, err := s.DB.CertificateForAttempt(ctx, *attemptID); err == nil {
			return existing, nil
		} else if !isNotFound(err) {
			return nil, err
		}
	}
	// A learner who already holds a valid certificate for this course
	// keeps it; re-issuing on every pass would be noise.
	if existing, err := s.DB.CertificateForLearnerCourse(ctx, ac.LearnerID, ac.CourseID); err == nil {
		return existing, nil
	} else if !isNotFound(err) {
		return nil, err
	}

	completedAtStr := completedAt.UTC().Format(time.RFC3339)
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		cert, err := s.DB.IssueCertificate(ctx, store.IssueCertificateParams{
			OrgID:            orgID,
			LearnerID:        ac.LearnerID,
			CourseID:         ac.CourseID,
			CampaignID:       &ac.CampaignID,
			AttemptID:        attemptID,
			Score:            score,
			CompletedAt:      completedAtStr,
			LearnerName:      ac.LearnerName,
			CourseTitle:      ac.CourseTitle,
			VerificationCode: newVerificationCode(),
		})
		if err == nil {
			return cert, nil
		}
		lastErr = err
		// Only a verification-code collision is worth retrying; any other
		// constraint violation is a real problem and is surfaced as-is.
		if !isConflict(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a unique verification code: %w", lastErr)
}

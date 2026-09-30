package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// The paper
// ---------------------------------------------------------------------

// QuestionPaperForLearner returns the assessment with every answer key
// stripped. The stripping is a deliberate copy so a future code change
// cannot leak a correct flag by mutating a shared slice.
func (s *Service) QuestionPaperForLearner(ctx context.Context, orgID, learnerID, assignmentID uuid.UUID) (*domain.QuestionPaper, error) {
	ac, err := s.DB.AssignmentContext(ctx, orgID, assignmentID)
	if err != nil {
		return nil, err
	}
	if ac.LearnerID != learnerID {
		return nil, httpx.Forbidden("This training belongs to another learner.")
	}
	if ac.CampaignStatus != string(domain.CampaignActive) {
		return nil, httpx.Conflict("campaign_not_active", "This training campaign is no longer active.")
	}
	if ac.RequireLearning && !ac.LearningComplete {
		return nil, httpx.Conflict("learning_incomplete",
			"Finish the course content before opening the assessment.")
	}

	questions, err := s.DB.QuestionsForCourse(ctx, orgID, ac.CourseID)
	if err != nil {
		return nil, err
	}
	if len(questions) == 0 {
		return nil, httpx.Conflict("no_questions",
			"This course has no assessment questions yet.")
	}

	total := 0.0
	safe := make([]domain.Question, 0, len(questions))
	for _, q := range questions {
		total += q.Points
		safe = append(safe, learnerSafeQuestion(q))
	}

	return &domain.QuestionPaper{
		AssignmentID:  assignmentID,
		CourseID:      ac.CourseID,
		CourseTitle:   ac.CourseTitle,
		PassMark:      ac.PassMark,
		MaxAttempts:   ac.MaxAttempts,
		AttemptsUsed:  ac.AttemptsUsed,
		TotalPoints:   total,
		QuestionCount: len(safe),
		Questions:     safe,
	}, nil
}

// learnerSafeQuestion removes is_correct and explanation. The stripping
// lives on domain.Question so every learner-facing path shares one copy.
func learnerSafeQuestion(q domain.Question) domain.Question { return q.WithoutAnswers() }

// ---------------------------------------------------------------------
// Starting an attempt
// ---------------------------------------------------------------------

// StartAttempt opens a new attempt, enforcing the campaign's attempt
// limit and the learning requirement.
func (s *Service) StartAttempt(ctx context.Context, orgID, learnerID, assignmentID uuid.UUID) (*domain.Attempt, error) {
	ac, err := s.DB.AssignmentContext(ctx, orgID, assignmentID)
	if err != nil {
		return nil, err
	}
	if ac.LearnerID != learnerID {
		return nil, httpx.Forbidden("This training belongs to another learner.")
	}
	if ac.CampaignStatus != string(domain.CampaignActive) {
		return nil, httpx.Conflict("campaign_not_active", "This training campaign is no longer active.")
	}
	if ac.Status == domain.AssignPassed {
		return nil, httpx.Conflict("already_passed",
			"You have already passed this assessment.")
	}
	if ac.RequireLearning && !ac.LearningComplete {
		return nil, httpx.Conflict("learning_incomplete",
			"Finish the course content before starting the assessment.")
	}
	if ac.AttemptsUsed >= ac.MaxAttempts {
		return nil, httpx.Conflict("attempts_exhausted",
			fmt.Sprintf("You have used all %d attempts. Speak to your training administrator.", ac.MaxAttempts))
	}

	// Resume rather than stack: an attempt already in progress is the one
	// the learner should be answering.
	existing, _, err := s.DB.ListAttempts(ctx, orgID, store.AttemptFilter{
		AssignmentID: &assignmentID, Status: string(domain.AttemptInProgress),
		Page: 1, PerPage: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return s.DB.AttemptDetail(ctx, orgID, existing[0].ID)
	}

	attempt, err := s.DB.CreateAttempt(ctx, orgID, assignmentID, learnerID, ac.TotalPoints)
	if err != nil {
		return nil, err
	}
	_ = s.DB.TouchAssignment(ctx, assignmentID)
	return s.DB.AttemptDetail(ctx, orgID, attempt.ID)
}

// ---------------------------------------------------------------------
// Answering
// ---------------------------------------------------------------------

// SaveAnswerInput is one typed or selected response.
type SaveAnswerInput struct {
	QuestionID        uuid.UUID   `json:"question_id"`
	ResponseText      *string     `json:"response_text"`
	SelectedOptionIDs []uuid.UUID `json:"selected_option_ids"`
}

// SaveAnswer validates a response against its question and stores it.
//
// Length limits are enforced server side rather than only in the browser:
// a text answer outside min/max length is rejected outright, because a
// manual grader must be able to trust the length they were told about.
func (s *Service) SaveAnswer(ctx context.Context, orgID, learnerID, attemptID uuid.UUID, in []SaveAnswerInput) ([]domain.Answer, error) {
	attempt, err := s.DB.AttemptByID(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.LearnerID != learnerID {
		return nil, httpx.Forbidden("This attempt belongs to another learner.")
	}
	if attempt.Status != domain.AttemptInProgress {
		return nil, httpx.Conflict("attempt_closed",
			"This attempt has already been submitted and can no longer be changed.")
	}

	ac, err := s.DB.AssignmentContext(ctx, orgID, attempt.AssignmentID)
	if err != nil {
		return nil, err
	}
	questions, err := s.DB.QuestionsForCourse(ctx, orgID, ac.CourseID)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]domain.Question{}
	for _, q := range questions {
		byID[q.ID] = q
	}

	for _, a := range in {
		q, ok := byID[a.QuestionID]
		if !ok {
			return nil, httpx.FieldError("question_id",
				"That question is not part of this assessment.")
		}
		if err := validateAnswer(q, a); err != nil {
			return nil, err
		}
		if err := s.DB.SaveAnswer(ctx, store.UpsertAnswerParams{
			OrgID:             orgID,
			AttemptID:         attemptID,
			QuestionID:        a.QuestionID,
			ResponseText:      a.ResponseText,
			SelectedOptionIDs: a.SelectedOptionIDs,
		}); err != nil {
			return nil, err
		}
	}
	_ = s.DB.TouchAssignment(ctx, attempt.AssignmentID)
	answers, _, err := s.DB.AnswersForAttempt(ctx, orgID, attemptID)
	return answers, err
}

func validateAnswer(q domain.Question, a SaveAnswerInput) error {
	switch {
	case q.Type.IsChoice():
		if len(a.SelectedOptionIDs) == 0 {
			return httpx.FieldError("selected_option_ids", "Select at least one option.")
		}
		valid := map[uuid.UUID]bool{}
		for _, o := range q.Options {
			valid[o.ID] = true
		}
		seen := map[uuid.UUID]bool{}
		for _, id := range a.SelectedOptionIDs {
			if !valid[id] {
				return httpx.FieldError("selected_option_ids", "One of the selected options does not exist.")
			}
			if seen[id] {
				return httpx.FieldError("selected_option_ids", "The same option was selected twice.")
			}
			seen[id] = true
		}
		if q.Type == domain.QSingleChoice && len(a.SelectedOptionIDs) > 1 {
			return httpx.FieldError("selected_option_ids",
				"This question allows exactly one answer.")
		}
	case q.Type.IsText():
		text := ""
		if a.ResponseText != nil {
			text = strings.TrimSpace(*a.ResponseText)
		}
		if text == "" {
			if q.IsRequired {
				return httpx.FieldError("response_text", "This question requires an answer.")
			}
			return nil
		}
		n := utf8.RuneCountInString(text)
		if q.MinLength != nil && n < *q.MinLength {
			return ValidationError("Answer is too short.", map[string]any{
				"response_text": fmt.Sprintf("Write at least %d characters (you wrote %d).", *q.MinLength, n),
			})
		}
		if q.MaxLength != nil && n > *q.MaxLength {
			return ValidationError("Answer is too long.", map[string]any{
				"response_text": fmt.Sprintf("Use at most %d characters (you wrote %d).", *q.MaxLength, n),
			})
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Submission and grading
// ---------------------------------------------------------------------

// SubmitResult is returned by POST /attempts/{id}/submit.
type SubmitResult struct {
	Attempt     *domain.Attempt     `json:"attempt"`
	Passed      *bool               `json:"passed"`
	PassMark    float64             `json:"pass_mark"`
	AutoScore   float64             `json:"auto_score"`
	TotalPoints float64             `json:"total_points"`
	NeedsReview bool                `json:"needs_manual_review"`
	ManualCount int                 `json:"manual_grade_count"`
	Unanswered  []uuid.UUID         `json:"unanswered_questions,omitempty"`
	Certificate *domain.Certificate `json:"certificate,omitempty"`
	Message     string              `json:"message"`
	NextSteps   []string            `json:"next_steps,omitempty"`
}

// Submit grades what can be graded automatically, records the result and,
// when manual questions remain, parks the attempt for review.
//
// Retakes are explicitly allowed after a failure, which is step 11 of the
// acceptance test.
func (s *Service) Submit(ctx context.Context, orgID, learnerID, attemptID uuid.UUID) (*SubmitResult, error) {
	attempt, err := s.DB.AttemptByID(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.LearnerID != learnerID {
		return nil, httpx.Forbidden("This attempt belongs to another learner.")
	}
	if attempt.Status != domain.AttemptInProgress {
		return nil, httpx.Conflict("attempt_closed", "This attempt has already been submitted.")
	}

	ac, err := s.DB.AssignmentContext(ctx, orgID, attempt.AssignmentID)
	if err != nil {
		return nil, err
	}
	questions, err := s.DB.QuestionsForCourse(ctx, orgID, ac.CourseID)
	if err != nil {
		return nil, err
	}

	answers, _, err := s.DB.AnswersForAttempt(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	answered := map[uuid.UUID]domain.Answer{}
	for _, a := range answers {
		answered[a.QuestionID] = a
	}

	// Unanswered required questions are reported rather than silently
	// scored as zero, so the learner can resubmit within this attempt.
	var unanswered []uuid.UUID
	for _, q := range questions {
		if !q.IsRequired {
			continue
		}
		a, ok := answered[q.ID]
		if !ok || (!hasText(a.ResponseText) && len(a.SelectedOptionIDs) == 0) {
			unanswered = append(unanswered, q.ID)
		}
	}
	if len(unanswered) > 0 && len(unanswered) < len(questions) {
		return nil, ValidationError("Some required questions are unanswered.", map[string]any{
			"unanswered_questions": unanswered,
		})
	}
	if len(unanswered) == len(questions) && len(questions) > 0 {
		return nil, ValidationError("No answers were submitted.", map[string]any{
			"unanswered_questions": unanswered,
		})
	}

	// Auto-grade the choice questions.
	questionIDs := make([]uuid.UUID, 0, len(questions))
	for _, q := range questions {
		questionIDs = append(questionIDs, q.ID)
	}
	correct, err := s.DB.CorrectOptionIDs(ctx, questionIDs)
	if err != nil {
		return nil, err
	}

	var results []store.AutoGradeResult
	autoScore := 0.0
	manualPending := 0
	for _, q := range questions {
		if !q.Type.AutoGraded() {
			if a, ok := answered[q.ID]; !ok || a.PointsAwarded != nil {
				manualPending++
			}
			continue
		}
		a := answered[q.ID]
		right := correct[q.ID]
		isCorrect := sameSet(a.SelectedOptionIDs, right)
		points := 0.0
		if isCorrect {
			points = q.Points
			autoScore += points
		}
		results = append(results, store.AutoGradeResult{
			QuestionID: q.ID, IsCorrect: isCorrect, PointsAwarded: points,
		})
	}
	if err := s.DB.FinalizeAutoGraded(ctx, orgID, attemptID, results); err != nil {
		return nil, err
	}

	needsReview := manualPending > 0
	finalized, err := s.DB.MarkAttemptSubmitted(ctx, orgID, attemptID,
		autoScore, ac.TotalPoints, ac.PassMark, needsReview)
	if err != nil {
		return nil, err
	}

	result := &SubmitResult{
		Attempt:     finalized,
		PassMark:    ac.PassMark,
		AutoScore:   autoScore,
		TotalPoints: ac.TotalPoints,
		NeedsReview: needsReview,
		ManualCount: manualPending,
		Unanswered:  unanswered,
	}

	if needsReview {
		if err := s.DB.TouchAssignment(ctx, attempt.AssignmentID); err != nil {
			return nil, err
		}
		_ = s.DB.MarkAttemptSubmitted // status already set above
		result.Message = "Your answers are in. A manager will review the written answers and your result will be updated."
		result.NextSteps = []string{"You will see the result as soon as grading is complete."}
		return result, nil
	}

	// Fully auto-graded: settle the assignment and issue a certificate if
	// the learner passed and the campaign calls for one.
	passed := finalized.Status == domain.AttemptPassed
	result.Passed = &passed
	if err := s.DB.ApplyAttemptOutcome(ctx, orgID, attempt.AssignmentID, finalized.Percent, passed); err != nil {
		return nil, err
	}
	if passed {
		result.Message = fmt.Sprintf("You passed with %.2f%%.", finalized.Percent)
		if ac.IssueCertificate {
			cert, err := s.issueCertificateFor(ctx, orgID, ac, &attemptID, finalized.Percent, *finalized.SubmittedAt)
			if err != nil {
				return nil, err
			}
			result.Certificate = cert
			result.Message += " Your certificate is ready."
		}
		result.NextSteps = []string{"View your certificate.", "Take another assigned course."}
	} else {
		remaining := ac.MaxAttempts - (ac.AttemptsUsed)
		result.Message = fmt.Sprintf("You scored %.2f%%. The pass mark is %.2f%%.",
			finalized.Percent, ac.PassMark)
		if remaining > 0 {
			result.Message += fmt.Sprintf(" You have %d attempt(s) remaining.", remaining)
			result.NextSteps = []string{"Review the course content and try again."}
		} else {
			result.Message += " You have no attempts remaining."
			result.NextSteps = []string{"Contact your training administrator to arrange a retake."}
		}
	}
	return result, nil
}

// hasText reports whether a nullable response actually contains
// something other than whitespace.
func hasText(s *string) bool { return s != nil && strings.TrimSpace(*s) != "" }

// sameSet compares two option-id sets, ignoring order and duplicates.
func sameSet(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[uuid.UUID]int, len(a))
	for _, id := range a {
		set[id]++
	}
	for _, id := range b {
		set[id]--
		if set[id] < 0 {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------
// Manual grading
// ---------------------------------------------------------------------

// GradeInput is a manager's verdict on one written answer.
type GradeInput struct {
	AnswerID  uuid.UUID `json:"answer_id"`
	Points    float64   `json:"points"`
	Feedback  *string   `json:"feedback"`
	IsCorrect *bool     `json:"is_correct"`
}

// GradeResult reports the outcome of grading a batch of answers.
type GradeResult struct {
	Attempt      *domain.Attempt     `json:"attempt"`
	Passed       *bool               `json:"passed"`
	PassMark     float64             `json:"pass_mark"`
	Score        float64             `json:"score"`
	Percent      float64             `json:"percent"`
	StillPending int                 `json:"still_pending"`
	Certificate  *domain.Certificate `json:"certificate,omitempty"`
	Message      string              `json:"message"`
}

// GradeAnswers applies manual grades. The attempt is only finalised once
// every written answer has been graded, so a partial grading round never
// produces a premature result.
func (s *Service) GradeAnswers(ctx context.Context, orgID, graderID, attemptID uuid.UUID, grades []GradeInput) (*GradeResult, error) {
	attempt, err := s.DB.AttemptByID(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.Status == domain.AttemptInProgress {
		return nil, httpx.Conflict("attempt_open",
			"This attempt has not been submitted yet, so it cannot be graded.")
	}

	ac, err := s.DB.AssignmentContext(ctx, orgID, attempt.AssignmentID)
	if err != nil {
		return nil, err
	}
	questions, err := s.DB.QuestionsForCourse(ctx, orgID, ac.CourseID)
	if err != nil {
		return nil, err
	}
	pointsOf := map[uuid.UUID]float64{}
	for _, q := range questions {
		pointsOf[q.ID] = q.Points
	}

	for _, g := range grades {
		answer, err := s.DB.AnswerByID(ctx, orgID, g.AnswerID)
		if err != nil {
			return nil, err
		}
		if answer.AttemptID != attemptID {
			return nil, httpx.FieldError("answer_id",
				"That answer belongs to a different attempt.")
		}
		ceiling, known := pointsOf[answer.QuestionID]
		if !known {
			return nil, httpx.FieldError("answer_id",
				"That answer is not part of this assessment.")
		}
		if g.Points < 0 {
			return nil, ValidationError("Points cannot be negative.", map[string]any{
				"points": "Enter zero or more points.",
			})
		}
		if g.Points > ceiling {
			return nil, ValidationError("Points exceed the maximum for this question.", map[string]any{
				"points": fmt.Sprintf("This question is worth %.2f points.", ceiling),
			})
		}
		if err := s.DB.GradeAnswer(ctx, store.GradeAnswerParams{
			OrgID:     orgID,
			AnswerID:  g.AnswerID,
			GradedBy:  graderID,
			Points:    g.Points,
			Feedback:  g.Feedback,
			IsCorrect: g.IsCorrect,
		}); err != nil {
			return nil, err
		}
	}

	pending, err := s.DB.AnswersNeedingManual(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	result := &GradeResult{StillPending: len(pending)}

	if len(pending) > 0 {
		detail, err := s.DB.AttemptDetail(ctx, orgID, attemptID)
		if err != nil {
			return nil, err
		}
		result.Attempt = detail
		result.Message = fmt.Sprintf("%d written answer(s) still need a grade.", len(pending))
		return result, nil
	}

	finalized, err := s.DB.FinalizeGradedAttempt(ctx, orgID, attemptID, ac.PassMark)
	if err != nil {
		return nil, err
	}
	passed := finalized.Status == domain.AttemptPassed
	result.Attempt = finalized
	result.Passed = &passed
	result.PassMark = ac.PassMark
	result.Score = finalized.Score
	result.Percent = finalized.Percent

	if err := s.DB.ApplyAttemptOutcome(ctx, orgID, attempt.AssignmentID, finalized.Percent, passed); err != nil {
		return nil, err
	}
	if passed && ac.IssueCertificate {
		completedAt := time.Now().UTC()
		if finalized.SubmittedAt != nil {
			completedAt = *finalized.SubmittedAt
		}
		cert, err := s.issueCertificateFor(ctx, orgID, ac, &attemptID, finalized.Percent, completedAt)
		if err != nil {
			return nil, err
		}
		result.Certificate = cert
		result.Message = fmt.Sprintf("Graded. The learner passed with %.2f%% and has been issued a certificate.", finalized.Percent)
	} else if passed {
		result.Message = fmt.Sprintf("Graded. The learner passed with %.2f%%.", finalized.Percent)
	} else {
		result.Message = fmt.Sprintf("Graded. The learner scored %.2f%% and did not reach the %.2f%% pass mark.",
			finalized.Percent, ac.PassMark)
	}
	return result, nil
}

// AnswersForAttempt exposes a learner's saved responses so the paper can
// be rehydrated when they return mid-attempt.
func (s *Service) AnswersForAttempt(ctx context.Context, orgID, learnerID, attemptID uuid.UUID) ([]domain.Answer, error) {
	attempt, err := s.DB.AttemptByID(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.LearnerID != learnerID {
		return nil, httpx.Forbidden("This attempt belongs to another learner.")
	}
	// The answer key is attached only for submitted attempts, so a
	// learner still working cannot read the correct options back.
	answers, _, err := s.DB.AnswersForAttempt(ctx, orgID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.Status == domain.AttemptInProgress {
		for i := range answers {
			answers[i].Question = nil
			answers[i].Options = nil
		}
	}
	return answers, nil
}

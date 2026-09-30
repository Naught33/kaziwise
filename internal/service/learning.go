package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// Publishing
// ---------------------------------------------------------------------

// PublishCourse validates that a course is teachable, flips it to
// published and stamps published_at.
//
// Validation is explicit rather than implicit because a published course
// can already be assigned to a learner: refusing here is much cheaper than
// discovering a course with no lessons mid-campaign.
func (s *Service) PublishCourse(ctx context.Context, orgID, courseID uuid.UUID) (*domain.Course, error) {
	course, err := s.DB.CourseByID(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	if course.Status == domain.CoursePublished {
		return course, nil
	}

	lessons, err := s.DB.ContentLessons(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	if len(lessons) == 0 {
		return nil, ValidationError("This course cannot be published yet.", map[string]any{
			"lessons": "Add at least one module with a lesson that has content.",
		})
	}
	for _, l := range lessons {
		if len(l.Title) == 0 {
			return nil, ValidationError("This course cannot be published yet.", map[string]any{
				"lessons": fmt.Sprintf("Lesson %q has no title.", l.Title),
			})
		}
	}

	published := domain.CoursePublished
	return s.DB.UpdateCourse(ctx, orgID, courseID, store.UpdateCourseParams{Status: &published})
}

// UnpublishCourse returns a published course to draft. Refused while a
// campaign is actively assigning it, so live tracking is never lost.
func (s *Service) UnpublishCourse(ctx context.Context, orgID, courseID uuid.UUID) (*domain.Course, error) {
	var active int
	if err := s.DB.Pool().QueryRow(ctx, `
		select count(*) from campaigns
		where org_id = $1 and course_id = $2 and status = 'active'`, orgID, courseID).
		Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, httpx.Conflict("course_in_use",
			"This course is running in an active campaign. Close the campaign first.")
	}
	draft := domain.CourseDraft
	return s.DB.UpdateCourse(ctx, orgID, courseID, store.UpdateCourseParams{Status: &draft})
}

// ---------------------------------------------------------------------
// Campaign launch
// ---------------------------------------------------------------------

// LaunchCampaignResult reports what a launch actually did.
type LaunchCampaignResult struct {
	Campaign *domain.Campaign `json:"campaign"`
	Assigned int              `json:"assigned_count"`
	Skipped  int              `json:"skipped_count"`
	FirstRun bool             `json:"first_launch"`
}

// LaunchCampaign creates the learner assignment rows. Re-launching an
// active campaign is a no-op for existing learners and picks up anyone
// newly added to the audience, which is how KaziWise handles late joiners.
func (s *Service) LaunchCampaign(ctx context.Context, orgID, campaignID uuid.UUID) (*LaunchCampaignResult, error) {
	campaign, err := s.DB.CampaignByID(ctx, orgID, campaignID)
	if err != nil {
		return nil, err
	}
	if campaign.Status == domain.CampaignClosed {
		return nil, httpx.Conflict("campaign_closed", "This campaign is closed and cannot be launched.")
	}

	// The course must be published: assigning an unpublished course would
	// create a learner task that cannot be opened.
	course, err := s.DB.CourseByID(ctx, orgID, campaign.CourseID)
	if err != nil {
		return nil, err
	}
	if course.Status != domain.CoursePublished {
		return nil, httpx.Conflict("course_not_published",
			"Publish the course before launching the campaign.")
	}

	preview, err := s.DB.CampaignPreviewAudience(ctx, orgID, campaignID)
	if err != nil {
		return nil, err
	}
	if preview == 0 {
		return nil, ValidationError("Nobody would be assigned by this campaign.", map[string]any{
			"audience": "Choose a department that has active learners, or select employees explicitly.",
		})
	}

	assigned, err := s.DB.LaunchCampaign(ctx, orgID, campaignID)
	if err != nil {
		return nil, err
	}
	if err := s.DB.RecomputeAllProgress(ctx, orgID, campaignID); err != nil {
		return nil, err
	}
	updated, err := s.DB.CampaignByID(ctx, orgID, campaignID)
	if err != nil {
		return nil, err
	}
	return &LaunchCampaignResult{
		Campaign: updated,
		Assigned: assigned,
		Skipped:  preview - assigned,
		FirstRun: campaign.LaunchedAt == nil,
	}, nil
}

// CloseCampaign stops assigning new work but keeps every result and
// certificate already recorded.
func (s *Service) CloseCampaign(ctx context.Context, orgID, campaignID uuid.UUID) (*domain.Campaign, error) {
	if err := s.DB.CloseCampaign(ctx, orgID, campaignID); err != nil {
		return nil, err
	}
	return s.DB.CampaignByID(ctx, orgID, campaignID)
}

// ---------------------------------------------------------------------
// Learner progress
// ---------------------------------------------------------------------

// StartLesson notes that a learner opened a lesson, which flips the
// assignment to in_progress and records last activity.
func (s *Service) StartLesson(ctx context.Context, orgID, learnerID, lessonID uuid.UUID) error {
	if err := s.DB.StartLesson(ctx, orgID, lessonID, learnerID); err != nil {
		return err
	}
	// Touch every live assignment of this learner on this course so the
	// dashboard's last-activity column is honest.
	assignments, _, err := s.DB.ListAssignments(ctx, orgID, store.AssignmentFilter{
		LearnerID: &learnerID,
		Page:      1, PerPage: 50,
	})
	if err != nil {
		return err
	}
	lesson, err := s.DB.LessonByID(ctx, orgID, lessonID)
	if err != nil {
		return err
	}
	for _, a := range assignments {
		if a.CourseID == lesson.CourseID && a.Status != domain.AssignPassed {
			if err := s.DB.TouchAssignment(ctx, a.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// LessonCompletion is the response to "Mark Complete & Continue".
type LessonCompletion struct {
	LessonID           uuid.UUID  `json:"lesson_id"`
	AssignmentID       uuid.UUID  `json:"assignment_id"`
	Completed          bool       `json:"completed"`
	LessonsDone        int        `json:"lessons_done"`
	LessonsTotal       int        `json:"lessons_total"`
	ProgressPercent    float64    `json:"progress_percent"`
	LearningComplete   bool       `json:"learning_complete"`
	AssessmentUnlocked bool       `json:"assessment_unlocked"`
	NextLessonID       *uuid.UUID `json:"next_lesson_id,omitempty"`
	CourseCompleted    bool       `json:"course_completed"`
}

// CompleteLesson marks a lesson done, recomputes the rollup and reports
// whether the assessment is now unlocked, which is what the player needs
// to decide between "next lesson" and "start assessment".
func (s *Service) CompleteLesson(ctx context.Context, orgID, learnerID, assignmentID, lessonID uuid.UUID) (*LessonCompletion, error) {
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
	lesson, err := s.DB.LessonByID(ctx, orgID, lessonID)
	if err != nil {
		return nil, err
	}
	if lesson.CourseID != ac.CourseID {
		return nil, httpx.BadRequest("lesson_not_in_course",
			"That lesson is not part of the assigned course.")
	}

	// The lesson must actually have content, otherwise a client could mark
	// an empty placeholder complete and skip the learning requirement.
	blocks, err := s.DB.ListBlocks(ctx, orgID, lessonID)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, httpx.Conflict("lesson_empty",
			"This lesson has no content yet, so it cannot be marked complete.")
	}

	if err := s.DB.CompleteLesson(ctx, orgID, lessonID, learnerID); err != nil {
		return nil, err
	}
	if err := s.DB.TouchAssignment(ctx, assignmentID); err != nil {
		return nil, err
	}

	refreshed, err := s.DB.AssignmentContext(ctx, orgID, assignmentID)
	if err != nil {
		return nil, err
	}
	complete := refreshed.LearningComplete
	unlocked := !refreshed.RequireLearning || complete

	next, err := s.nextLessonID(ctx, orgID, lessonID)
	if err != nil {
		return nil, err
	}
	return &LessonCompletion{
		LessonID:           lessonID,
		AssignmentID:       assignmentID,
		Completed:          true,
		LessonsDone:        refreshed.LessonsDone,
		LessonsTotal:       refreshed.LessonsTotal,
		ProgressPercent:    percentOfDone(refreshed.LessonsDone, refreshed.LessonsTotal),
		LearningComplete:   complete,
		AssessmentUnlocked: unlocked,
		NextLessonID:       next,
		CourseCompleted:    complete,
	}, nil
}

func percentOfDone(done, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(int(float64(done)/float64(total)*100*100+0.5)) / 100
}

// nextLessonID walks the course outline in reading order and returns the
// lesson after the given one, or nil at the end.
func (s *Service) nextLessonID(ctx context.Context, orgID, lessonID uuid.UUID) (*uuid.UUID, error) {
	lesson, err := s.DB.LessonByID(ctx, orgID, lessonID)
	if err != nil {
		return nil, err
	}
	lessons, err := s.DB.ListLessons(ctx, orgID, lesson.CourseID)
	if err != nil {
		return nil, err
	}
	for i, l := range lessons {
		if l.ID == lessonID && i+1 < len(lessons) {
			next := lessons[i+1].ID
			return &next, nil
		}
	}
	return nil, nil
}

// RecordPageView notes that a learner has seen a page of a paginated
// block. It is idempotent and never fails the request: progress
// telemetry must not break content delivery.
func (s *Service) RecordPageView(ctx context.Context, orgID, learnerID, blockID uuid.UUID, pages []int) {
	for _, p := range pages {
		if p <= 0 {
			continue
		}
		if err := s.DB.RecordBlockPageSeen(ctx, orgID, blockID, learnerID, p); err != nil && s.Log != nil {
			s.Log.Warn("could not record page view", "block_id", blockID, "page", p, "error", err)
		}
	}
}

// ---------------------------------------------------------------------
// Learner dashboard
// ---------------------------------------------------------------------

// LearnerDashboard assembles the screen-08 payload.
func (s *Service) LearnerDashboard(ctx context.Context, orgID, learnerID uuid.UUID) (*domain.LearnerDashboard, error) {
	user, err := s.DB.UserByID(ctx, orgID, learnerID)
	if err != nil {
		return nil, err
	}
	assigned, inProgress, completed, passed, failed, overdue, progress, err :=
		s.DB.LearnerSummary(ctx, orgID, learnerID)
	if err != nil {
		return nil, err
	}

	// Required training is everything not yet passed, most urgent first.
	todo, _, err := s.DB.ListAssignments(ctx, orgID, store.AssignmentFilter{
		LearnerID: &learnerID, Page: 1, PerPage: 100, Sort: "due", Order: "asc",
	})
	if err != nil {
		return nil, err
	}
	required := []domain.Assignment{}
	for _, a := range todo {
		if a.Status != domain.AssignPassed {
			required = append(required, a)
		}
	}

	certs, _, err := s.DB.ListCertificates(ctx, orgID, &learnerID, 1, 20)
	if err != nil {
		return nil, err
	}
	if certs == nil {
		certs = []domain.Certificate{}
	}

	return &domain.LearnerDashboard{
		User:             *user,
		OverallProgress:  progress,
		AssignedCount:    assigned,
		InProgressCount:  inProgress,
		CompletedCount:   completed,
		PassedCount:      passed,
		FailedCount:      failed,
		OverdueCount:     overdue,
		RequiredTraining: required,
		Certificates:     certs,
		Greeting:         greeting(user.FullName),
	}, nil
}

func greeting(name string) string {
	first := strings.TrimSpace(name)
	if i := strings.IndexAny(first, " "); i > 0 {
		first = first[:i]
	}
	hour := time.Now().Hour()
	switch {
	case hour < 12:
		return "Good morning, " + first
	case hour < 18:
		return "Good afternoon, " + first
	default:
		return "Good evening, " + first
	}
}

// ---------------------------------------------------------------------
// Reminders
// ---------------------------------------------------------------------

// ReminderResult reports how many learners were chased.
type ReminderResult struct {
	Reminded int      `json:"reminded"`
	Skipped  int      `json:"skipped"`
	Details  []string `json:"details,omitempty"`
}

// RemindAssignment records a reminder for one learner.
func (s *Service) RemindAssignment(ctx context.Context, orgID, actorID, assignmentID uuid.UUID, note string) (*domain.Reminder, error) {
	assignment, err := s.DB.AssignmentByID(ctx, orgID, assignmentID)
	if err != nil {
		return nil, err
	}
	if assignment.Status == domain.AssignPassed {
		return nil, httpx.Conflict("already_passed",
			"This learner has already passed, so no reminder is needed.")
	}
	if note == "" {
		note = fmt.Sprintf("Please complete %q%s.", assignment.CourseTitle, duePhrase(assignment.DueDate))
	}
	subject := "Training reminder: " + assignment.CourseTitle
	reminder, err := s.DB.CreateReminder(ctx, orgID, domain.Reminder{
		AssignmentID: &assignmentID,
		LearnerID:    &assignment.LearnerID,
		CampaignID:   &assignment.CampaignID,
		Channel:      "in_app",
		Subject:      &subject,
		Body:         &note,
		SentBy:       &actorID,
	})
	if err != nil {
		return nil, err
	}
	_ = s.DB.MarkReminderSent(ctx, assignmentID)
	return reminder, nil
}

// RemindCampaign chases every unfinished learner in a campaign.
func (s *Service) RemindCampaign(ctx context.Context, orgID, actorID, campaignID uuid.UUID) (*ReminderResult, error) {
	assignments, _, err := s.DB.ListAssignments(ctx, orgID, store.AssignmentFilter{
		CampaignID: &campaignID, Page: 1, PerPage: 10000,
	})
	if err != nil {
		return nil, err
	}
	result := &ReminderResult{Details: []string{}}
	for _, a := range assignments {
		if a.Status == domain.AssignPassed {
			result.Skipped++
			continue
		}
		reminder, err := s.RemindAssignment(ctx, orgID, actorID, a.ID, "")
		if err != nil {
			result.Details = append(result.Details,
				fmt.Sprintf("%s: %v", a.LearnerName, err))
			continue
		}
		_ = reminder
		result.Reminded++
	}
	return result, nil
}

func duePhrase(due *time.Time) string {
	if due == nil {
		return ""
	}
	days := int(time.Until(*due).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf(" - it was due %s", due.Format("2 Jan 2006"))
	case days == 0:
		return " - it is due today"
	case days == 1:
		return " - it is due tomorrow"
	default:
		return fmt.Sprintf(" - it is due in %d days", days)
	}
}

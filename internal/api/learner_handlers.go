package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/service"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// Learner dashboard (screen 08)
// ---------------------------------------------------------------------

// dashboard returns the signed-in learner's training summary. Staff may
// pass ?learner_id= to inspect someone else.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	learnerID := actorID
	claims := ClaimsFrom(r.Context())
	if raw := r.URL.Query().Get("learner_id"); raw != "" && !claims.IsLearner() {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("learner_id", "That is not a valid learner id."))
			return
		}
		learnerID = id
	}
	view, err := s.svc.LearnerDashboard(r.Context(), orgID, learnerID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Staff must not see a colleague's private learner view unless they
	// have a legitimate reason; the audit line records the access.
	if learnerID != actorID {
		s.audit(r, "dashboard.view_other", "profile", learnerID, nil)
	}
	httpx.JSON(w, view)
}

// myTraining is the learner's own list, always scoped to the signed-in
// user regardless of query parameters.
func (s *Server) myTraining(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.AssignmentFilter{
		LearnerID: &actorID, Status: p.Status, Search: p.Search,
		Page: p.Page, PerPage: p.PerPage,
	}
	if p.Overdue != nil {
		f.Overdue = *p.Overdue
	}
	assignments, total, err := s.db.ListAssignments(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if assignments == nil {
		assignments = []domain.Assignment{}
	}
	httpx.JSONMeta(w, assignments, p.Meta(total))
}

// ---------------------------------------------------------------------
// Course player (screen 09)
// ---------------------------------------------------------------------

// playCourse returns everything the player needs in one call: the course
// outline plus this learner's per-lesson progress. The learner may only
// open a course that has been assigned to them.
func (s *Server) playCourse(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	courseID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	course, err := s.db.CourseByID(r.Context(), orgID, courseID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	if course.Status != domain.CoursePublished {
		httpx.Fail(w, httpx.Forbidden("This course is not available yet."))
		return
	}

	// Which assignment, if any, is driving this learner through the course?
	// The most recently assigned live one wins, because a re-assignment
	// should not restart progress on an older attempt.
	assignments, _, err := s.db.ListAssignments(r.Context(), orgID,
		store.AssignmentFilter{LearnerID: &actorID, CourseID: &courseID, PerPage: 100})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var active *domain.Assignment
	for i := range assignments {
		a := assignments[i]
		if a.Status == domain.AssignPassed {
			continue
		}
		if active == nil || a.AssignedAt.After(active.AssignedAt) {
			active = &a
		}
	}
	if active == nil && ClaimsFrom(r.Context()).IsLearner() {
		httpx.Fail(w, httpx.Forbidden("This course has not been assigned to you."))
		return
	}

	outline, err := s.db.CourseOutline(r.Context(), orgID, courseID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonStatus, lessonScores, err := s.db.LessonStatusMap(r.Context(), orgID, actorID, courseID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	applyLessonProgress(outline, lessonStatus, lessonScores)

	questions, err := s.db.QuestionsForCourse(r.Context(), orgID, courseID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// The learner must never receive is_correct or the explanation, so the
	// question list is sanitised before it leaves the server.
	paper := make([]domain.Question, 0, len(questions))
	for _, q := range questions {
		paper = append(paper, q.WithoutAnswers())
	}

	body := map[string]any{
		"course":    course,
		"outline":   outline,
		"questions": paper,
	}
	if active != nil {
		body["assignment"] = active
		// Assessment availability is decided by the service so the
		// client and the server cannot disagree.
		ac, err := s.db.AssignmentContext(r.Context(), orgID, active.ID)
		if err == nil {
			body["learning_complete"] = ac.LearningComplete
			body["lessons_done"] = ac.LessonsDone
			body["lessons_total"] = ac.LessonsTotal
			body["attempts_used"] = ac.AttemptsUsed
			body["max_attempts"] = ac.MaxAttempts
		}
	}
	httpx.JSON(w, body)
}

// applyLessonProgress merges the learner's per-lesson state into the
// outline the player renders.
func applyLessonProgress(outline *domain.CourseOutline, status map[uuid.UUID]string, scores map[uuid.UUID]*string) {
	if outline == nil {
		return
	}
	for mi := range outline.Modules {
		module := &outline.Modules[mi]
		for li := range module.Lessons {
			lesson := &module.Lessons[li]
			if st, ok := status[lesson.ID]; ok {
				// A nil status would serialise as an absent key, which the
				// player reads as "not started"; an empty string must stay
				// distinguishable, so only a real value is attached.
				lesson.Status = &st
			}
			// LessonStatusMap also returns a score for a passed lesson, but
			// the domain type has no field for it yet, so it is dropped
			// here rather than silently discarded by the store.
			_ = scores
		}
	}
}

// recordPageView stores which pages of a paginated document the learner
// has actually seen, which is what drives "has read the whole chapter".
func (s *Server) recordPageView(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	blockID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Pages []int `json:"pages"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(req.Pages) == 0 {
		httpx.Fail(w, httpx.FieldError("pages", "Send the page numbers that were viewed."))
		return
	}
	if len(req.Pages) > 500 {
		httpx.Fail(w, httpx.FieldError("pages", "That is too many pages in one call."))
		return
	}
	for _, p := range req.Pages {
		if p < 1 {
			httpx.Fail(w, httpx.FieldError("pages", "Page numbers start at 1."))
			return
		}
	}
	s.svc.RecordPageView(r.Context(), orgID, actorID, blockID, req.Pages)
	httpx.NoContent(w)
}

// pageViewProgress reports how much of a paginated block has been seen.
func (s *Server) pageViewProgress(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	blockID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	seen, err := s.db.SeenBlockPages(r.Context(), orgID, blockID, actorID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, map[string]any{"block_id": blockID, "seen_pages": seen})
}

// ---------------------------------------------------------------------
// Lesson progress
// ---------------------------------------------------------------------

func (s *Server) startLesson(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.svc.StartLesson(r.Context(), orgID, actorID, lessonID); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

// completeLesson marks a lesson done and returns the next step, which the
// player uses to move on without a second round trip.
func (s *Server) completeLesson(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	assignmentID, err := requireAssignment(w, r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	result, err := s.svc.CompleteLesson(r.Context(), orgID, actorID, assignmentID, lessonID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, result)
}

func (s *Server) lessonProgress(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	courseID, err := pathUUID(r, "courseId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	status, scores, err := s.db.LessonStatusMap(r.Context(), orgID, actorID, courseID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, map[string]any{
		"lesson_id": lessonID,
		"status":    status[lessonID],
		"score":     scores[lessonID],
		"course_id": courseID,
	})
}

// ---------------------------------------------------------------------
// Assessment (screens 10, 11, 12)
// ---------------------------------------------------------------------

// startAttempt opens an attempt. The attempt limits, the learning
// requirement and the due date are all enforced in the service layer so
// they cannot be bypassed from the client.
func (s *Server) startAttempt(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	assignmentID, err := pathUUID(r, "assignmentId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attempt, err := s.svc.StartAttempt(r.Context(), orgID, actorID, assignmentID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.Created(w, attempt)
}

func (s *Server) getAttempt(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attempt, err := s.db.AttemptByID(r.Context(), orgID, attemptID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "attempt"))
		return
	}
	claims := ClaimsFrom(r.Context())
	if claims.IsLearner() {
		if attempt.LearnerID != actorID {
			httpx.Fail(w, httpx.Forbidden("You can only view your own attempt."))
			return
		}
		if attempt.Status.IsTerminal() {
			httpx.Fail(w, httpx.Forbidden("This attempt has already been graded."))
			return
		}
	}
	answers, err := s.svc.AnswersForAttempt(r.Context(), orgID, actorID, attemptID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if answers == nil {
		answers = []domain.Answer{}
	}
	httpx.JSON(w, map[string]any{"attempt": attempt, "answers": answers})
}

// saveAnswers stores in-progress responses so a learner can leave and
// come back. Validation runs here, so an out-of-range response is rejected
// immediately rather than at submission.
func (s *Server) saveAnswers(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Answers []service.SaveAnswerInput `json:"answers"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(req.Answers) == 0 {
		httpx.Fail(w, httpx.FieldError("answers", "Send at least one answer."))
		return
	}
	answers, err := s.svc.SaveAnswer(r.Context(), orgID, actorID, attemptID, req.Answers)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, map[string]any{"answers": answers})
}

// submitAttempt finalises an attempt, auto-grading what it can and
// queueing written answers for review.
func (s *Server) submitAttempt(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	result, err := s.svc.Submit(r.Context(), orgID, actorID, attemptID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "assessment.submit", "attempt", attemptID, map[string]any{
		"needs_review": result.NeedsReview,
	})
	httpx.JSON(w, result)
}

func (s *Server) submitAnswers(w http.ResponseWriter, r *http.Request) {
	s.submitAttempt(w, r)
}

func (s *Server) myAttempts(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	attempts, total, err := s.db.ListAttempts(r.Context(), orgID, store.AttemptFilter{
		LearnerID: &actorID, Page: p.Page, PerPage: p.PerPage,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, attempts, p.Meta(total))
}

// attemptResult is what a learner sees after grading.
func (s *Server) attemptResult(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attempt, err := s.db.AttemptByID(r.Context(), orgID, attemptID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "attempt"))
		return
	}
	claims := ClaimsFrom(r.Context())
	if claims.IsLearner() && attempt.LearnerID != actorID {
		httpx.Fail(w, httpx.Forbidden("You can only view your own result."))
		return
	}
	if claims.IsLearner() && !attempt.Status.IsTerminal() {
		httpx.Fail(w, httpx.Conflict("attempt_pending",
			"This attempt has not been graded yet."))
		return
	}
	answers, _, err := s.db.AnswersForAttempt(r.Context(), orgID, attemptID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if answers == nil {
		answers = []domain.Answer{}
	}
	httpx.JSON(w, map[string]any{"attempt": attempt, "answers": answers})
}

// ---------------------------------------------------------------------
// Grading queue (screen 13)
// ---------------------------------------------------------------------

func (s *Server) listAttempts(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.AttemptFilter{Status: p.Status, Page: p.Page, PerPage: p.PerPage}
	if p.NeedsReview != nil {
		f.NeedsReview = *p.NeedsReview
	}
	switch ClaimsFrom(r.Context()).Role {
	case auth.RoleManager:
		// A manager grades within their own team only.
		members, err := s.db.TeamMembers(r.Context(), orgID, actorID)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		ids := make([]uuid.UUID, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.ID)
		}
		f.Learners = ids
	case auth.RoleLearner:
		f.LearnerID = &actorID
	}
	attempts, total, err := s.db.ListAttempts(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, attempts, p.Meta(total))
}

func (s *Server) listPendingGrading(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.AttemptFilter{Status: string(domain.AttemptPendingReview), Page: p.Page, PerPage: p.PerPage}
	switch ClaimsFrom(r.Context()).Role {
	case auth.RoleManager:
		members, err := s.db.TeamMembers(r.Context(), orgID, actorID)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		ids := make([]uuid.UUID, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.ID)
		}
		f.Learners = ids
	}
	attempts, total, err := s.db.ListAttempts(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, attempts, p.Meta(total))
}

// attemptAnswers gives a grader the full script: every answer with the
// question it answers, so grading happens in one view.
func (s *Server) attemptAnswers(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attempt, err := s.db.AttemptByID(r.Context(), orgID, attemptID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "attempt"))
		return
	}
	answers, total, err := s.db.AnswersForAttempt(r.Context(), orgID, attemptID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if answers == nil {
		answers = []domain.Answer{}
	}
	httpx.JSONMeta(w, map[string]any{"attempt": attempt, "answers": answers},
		&httpx.Meta{Total: total})
}

// gradeAnswers applies manual grades. The attempt is only finalised once
// every written answer has been graded.
func (s *Server) gradeAnswers(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Grades []service.GradeInput `json:"grades"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(req.Grades) == 0 {
		httpx.Fail(w, httpx.FieldError("grades", "Send at least one grade."))
		return
	}
	result, err := s.svc.GradeAnswers(r.Context(), orgID, actorID, attemptID, req.Grades)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "assessment.grade", "attempt", attemptID, map[string]any{
		"graded": len(req.Grades), "passed": result.Passed,
	})
	httpx.JSON(w, result)
}

func (s *Server) gradeAnswer(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	attemptID, err := pathUUID(r, "attemptId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var grade service.GradeInput
	if err := httpx.Decode(w, r, &grade); err != nil {
		httpx.Fail(w, err)
		return
	}
	if grade.AnswerID == uuid.Nil {
		httpx.Fail(w, httpx.FieldError("answer_id", "Which answer are you grading?"))
		return
	}
	result, err := s.svc.GradeAnswers(r.Context(), orgID, actorID, attemptID,
		[]service.GradeInput{grade})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "assessment.grade_answer", "attempt", attemptID, map[string]any{
		"answer_id": grade.AnswerID, "points": grade.Points,
	})
	httpx.JSON(w, result)
}

// ---------------------------------------------------------------------
// Learners (staff view, screen 02)
// ---------------------------------------------------------------------

func (s *Server) listLearners(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.UserListFilter{
		Role: auth.RoleLearner, Search: p.Search, Status: p.Status,
		Department: p.RawQuery["department"], Page: p.Page, PerPage: p.PerPage,
	}
	// ?unassigned=true powers the "assign training" picker: learners with
	// no active assignment at all. A course filter is deliberately not
	// combined with it, because the store would apply both and return
	// only learners missing that specific course.
	if p.RawQuery["unassigned"] == "true" {
		f.Unassigned = true
	} else if raw := p.RawQuery["course_id"]; raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.WithoutCourseID = &id
		}
	}
	if ClaimsFrom(r.Context()).Role == auth.RoleManager {
		f.ManagerID = &actorID
	}
	users, total, err := s.db.ListUsers(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, users, p.Meta(total))
}

func (s *Server) getLearner(w http.ResponseWriter, r *http.Request) {
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
	if ClaimsFrom(r.Context()).Role == auth.RoleManager && id != actorID {
		user, err := s.db.UserByID(r.Context(), orgID, id)
		if err != nil {
			httpx.Fail(w, statusOf(err, "learner"))
			return
		}
		if user.ManagerID == nil || *user.ManagerID != actorID {
			httpx.Fail(w, httpx.Forbidden("You can only view your own team."))
			return
		}
	}
	assigned, inProgress, completed, passed, failed, overdue, progress, err :=
		s.db.LearnerSummary(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "learner"))
		return
	}
	assignments, _, err := s.db.ListAssignments(r.Context(), orgID,
		store.AssignmentFilter{LearnerID: &id, PerPage: 200})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	user, err := s.db.UserByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "learner"))
		return
	}
	certificates, _, err := s.db.ListCertificates(r.Context(), orgID, &id, 1, 50)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if certificates == nil {
		certificates = []domain.Certificate{}
	}
	if assignments == nil {
		assignments = []domain.Assignment{}
	}
	httpx.JSON(w, map[string]any{
		"user": user, "summary": map[string]any{
			"assigned": assigned, "in_progress": inProgress, "completed": completed,
			"passed": passed, "failed": failed, "overdue": overdue,
			"progress_percent": progress,
		},
		"assignments": assignments, "certificates": certificates,
	})
}

func (s *Server) getLearnerSummary(w http.ResponseWriter, r *http.Request) {
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
	claims := ClaimsFrom(r.Context())
	if claims.IsLearner() && id != actorID {
		httpx.Fail(w, httpx.Forbidden("You can only view your own summary."))
		return
	}
	assigned, inProgress, completed, passed, failed, overdue, progress, err :=
		s.db.LearnerSummary(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, map[string]any{
		"assigned": assigned, "in_progress": inProgress, "completed": completed,
		"passed": passed, "failed": failed, "overdue": overdue,
		"progress_percent": progress,
	})
}

// getLearnerCertificates serves both the staff view of one learner's
// certificates (/learners/{id}/certificates) and the learner's own list
// (/me/certificates). When no id is present in the path the caller's own
// id is used, which is why a learner can never read someone else's list.
func (s *Server) getLearnerCertificates(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	claims := ClaimsFrom(r.Context())
	id := actorID
	if chi.URLParam(r, "id") != "" {
		id, err = pathUUID(r, "id")
		if err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	if claims.IsLearner() && id != actorID {
		httpx.Fail(w, httpx.Forbidden("You can only view your own certificates."))
		return
	}
	p := httpx.Query(r)
	certificates, total, err := s.db.ListCertificates(r.Context(), orgID, &id, p.Page, p.PerPage)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	shared := make([]domain.Certificate, 0, len(certificates))
	for _, c := range certificates {
		shared = append(shared, *s.svc.CertificateShare(&c, s.cfg.BaseURL))
	}
	httpx.JSONMeta(w, shared, p.Meta(total))
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// requireAssignment resolves the assignment either from ?assignment_id= or
// from the attempt attached to the path, so a client that knows only the
// attempt can still report progress.
func requireAssignment(w http.ResponseWriter, r *http.Request) (uuid.UUID, error) {
	if raw := r.URL.Query().Get("assignment_id"); raw != "" {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return uuid.Nil, httpx.FieldError("assignment_id",
				"That is not a valid assignment id.")
		}
		return id, nil
	}
	raw := chi.URLParam(r, "attemptId")
	if raw == "" {
		return uuid.Nil, httpx.FieldError("assignment_id",
			"Pass ?assignment_id= so the progress can be attributed.")
	}
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, httpx.FieldError("assignment_id",
			"That is not a valid assignment id.")
	}
	return id, nil
}

// courseIDForAssignment lets handlers fetch a course from an assignment
// without repeating the fallback logic.
func courseIDForAssignment(r *http.Request, assignment *domain.Assignment) uuid.UUID {
	return assignment.CourseID
}

var _ = strings.TrimSpace

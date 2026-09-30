package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// Courses (screen 03)
// ---------------------------------------------------------------------

func (s *Server) listCourses(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	courses, total, err := s.db.ListCourses(r.Context(), orgID, store.CourseListFilter{
		Search:   p.Search,
		Status:   p.Status,
		Category: p.RawQuery["category"],
		Page:     p.Page,
		PerPage:  p.PerPage,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, courses, p.Meta(total))
}

func (s *Server) getCourse(w http.ResponseWriter, r *http.Request) {
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
	course, err := s.db.CourseByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	httpx.JSON(w, course)
}

type createCourseRequest struct {
	Title            string  `json:"title"`
	Code             *string `json:"code"`
	Description      *string `json:"description"`
	Category         *string `json:"category"`
	CoverAssetID     *string `json:"cover_asset_id"`
	EstimatedMinutes int     `json:"estimated_minutes"`
}

func (s *Server) createCourse(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req createCourseRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	title := strings.TrimSpace(req.Title)
	fields := map[string]any{}
	if title == "" {
		fields["title"] = "Enter a course title."
	} else if len([]rune(title)) < 3 {
		fields["title"] = "Use at least 3 characters."
	}
	if req.EstimatedMinutes < 0 {
		fields["estimated_minutes"] = "Duration cannot be negative."
	}
	if len(fields) > 0 {
		httpx.Fail(w, ValidationError("Please correct the highlighted fields.", fields))
		return
	}

	params := store.CreateCourseParams{
		OrgID: orgID, Title: title,
		Code: trimOrNil(deref(req.Code)), Description: trimOrNil(deref(req.Description)),
		Category:         trimOrNil(deref(req.Category)),
		EstimatedMinutes: req.EstimatedMinutes, CreatedBy: &actorID,
	}
	if raw := trimOrNil(deref(req.CoverAssetID)); raw != nil {
		aid, err := uuid.Parse(*raw)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("cover_asset_id", "That is not a valid asset id."))
			return
		}
		params.CoverAssetID = &aid
	}
	course, err := s.db.CreateCourse(r.Context(), params)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	s.audit(r, "course.create", "course", course.ID, map[string]any{"title": course.Title})
	httpx.Created(w, course)
}

type updateCourseRequest struct {
	Title            *string `json:"title"`
	Code             *string `json:"code"`
	Description      *string `json:"description"`
	Category         *string `json:"category"`
	CoverAssetID     *string `json:"cover_asset_id"`
	EstimatedMinutes *int    `json:"estimated_minutes"`
	Status           *string `json:"status"`
}

func (s *Server) updateCourse(w http.ResponseWriter, r *http.Request) {
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
	var req updateCourseRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Only touch the fields the client actually sent, so clearing one does
	// not depend on the others. See updateLesson for the same reasoning.
	p := store.UpdateCourseParams{}
	if req.Title != nil {
		p.Title = trimOrNil(*req.Title)
		if p.Title == nil {
			httpx.Fail(w, httpx.FieldError("title", "Enter a course title."))
			return
		}
	}
	if req.Code != nil {
		v := strings.TrimSpace(*req.Code)
		p.Code = &v
	}
	if req.Description != nil {
		v := strings.TrimSpace(*req.Description)
		p.Description = &v
	}
	if req.Category != nil {
		v := strings.TrimSpace(*req.Category)
		p.Category = &v
	}
	if req.EstimatedMinutes != nil {
		if *req.EstimatedMinutes < 0 {
			httpx.Fail(w, httpx.FieldError("estimated_minutes", "Duration cannot be negative."))
			return
		}
		p.EstimatedMinutes = req.EstimatedMinutes
	}
	if req.Status != nil {
		status := domain.CourseStatus(*req.Status)
		switch status {
		case domain.CourseDraft, domain.CoursePublished, domain.CourseArchived:
			p.Status = &status
		default:
			httpx.Fail(w, httpx.FieldError("status",
				"Status must be draft, published or archived."))
			return
		}
	}
	if raw := trimOrNil(deref(req.CoverAssetID)); raw != nil {
		aid, err := uuid.Parse(*raw)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("cover_asset_id", "That is not a valid asset id."))
			return
		}
		p.CoverAssetID = &aid
	}
	course, err := s.db.UpdateCourse(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	s.audit(r, "course.update", "course", id, nil)
	httpx.JSON(w, course)
}

func (s *Server) deleteCourse(w http.ResponseWriter, r *http.Request) {
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
	course, err := s.db.CourseByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	// A course with recorded results must be archived, not deleted, or the
	// history behind a learner's certificate disappears.
	if course.AssignedLearners > 0 {
		httpx.Fail(w, httpx.Conflict("course_in_use",
			"This course has been assigned to learners. Archive it instead of deleting."))
		return
	}
	if err := s.db.DeleteCourse(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	s.audit(r, "course.delete", "course", id, map[string]any{"title": course.Title})
	httpx.NoContent(w)
}

func (s *Server) publishCourse(w http.ResponseWriter, r *http.Request) {
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
	course, err := s.svc.PublishCourse(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "course.publish", "course", id, nil)
	httpx.JSON(w, course)
}

func (s *Server) unpublishCourse(w http.ResponseWriter, r *http.Request) {
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
	course, err := s.svc.UnpublishCourse(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "course.unpublish", "course", id, nil)
	httpx.JSON(w, course)
}

// courseOutline returns the full builder tree: modules, lessons, blocks and
// which pages are the blank chapter separators.
func (s *Server) courseOutline(w http.ResponseWriter, r *http.Request) {
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
	outline, err := s.db.CourseOutline(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	httpx.JSON(w, outline)
}

// ---------------------------------------------------------------------
// Modules
// ---------------------------------------------------------------------

type moduleRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Position    *int    `json:"position"`
}

func (s *Server) createModule(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	courseID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req moduleRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		httpx.Fail(w, ValidationError("A module needs a title.", map[string]any{
			"title": "Enter a module title.",
		}))
		return
	}
	position := 0
	if req.Position != nil {
		position = *req.Position
	}
	module, err := s.db.CreateModule(r.Context(), orgID, courseID, title,
		trimOrNil(deref(req.Description)), position)
	if err != nil {
		httpx.Fail(w, statusOf(err, "module"))
		return
	}
	httpx.Created(w, module)
}

type updateModuleRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Position    *int    `json:"position"`
}

func (s *Server) updateModule(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "moduleId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req updateModuleRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Only the fields the client sent are touched, so clearing the
	// description does not depend on the title being present too.
	p := store.UpdateModuleParams{Position: req.Position}
	if req.Title != nil {
		p.Title = trimOrNil(*req.Title)
		if p.Title == nil {
			httpx.Fail(w, httpx.FieldError("title", "A module needs a title."))
			return
		}
	}
	if req.Description != nil {
		v := strings.TrimSpace(*req.Description)
		p.Description = &v
	}
	module, err := s.db.UpdateModule(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "module"))
		return
	}
	httpx.JSON(w, module)
}

func (s *Server) deleteModule(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "moduleId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.db.DeleteModule(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "module"))
		return
	}
	httpx.NoContent(w)
}

// ---------------------------------------------------------------------
// Lessons
// ---------------------------------------------------------------------

func (s *Server) listLessons(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	moduleID, err := pathUUID(r, "moduleId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessons, err := s.db.ListLessons(r.Context(), orgID, moduleID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "module"))
		return
	}
	if lessons == nil {
		lessons = []domain.Lesson{}
	}
	httpx.JSON(w, lessons)
}

type createLessonRequest struct {
	Title            string  `json:"title"`
	Summary          *string `json:"summary"`
	Kind             string  `json:"kind"`
	Position         *int    `json:"position"`
	EstimatedMinutes int     `json:"estimated_minutes"`
}

func (s *Server) createLesson(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// The course id is in the path as {id} and must be forwarded to the
	// store: lessons.course_id is a not-null foreign key, so omitting it
	// fails the insert with a foreign key violation.
	courseID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	moduleID, err := pathUUID(r, "moduleId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req createLessonRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		httpx.Fail(w, ValidationError("A lesson needs a title.", map[string]any{
			"title": "Enter a lesson title.",
		}))
		return
	}
	kind := domain.LessonKind(req.Kind)
	if kind == "" {
		kind = domain.LessonContent
	}
	switch kind {
	case domain.LessonContent, domain.LessonAssessment, domain.LessonMixed:
	default:
		httpx.Fail(w, httpx.FieldError("kind",
			"Lesson kind must be content, assessment or mixed."))
		return
	}
	position := 0
	if req.Position != nil {
		position = *req.Position
	}
	lesson, err := s.db.CreateLesson(r.Context(), store.CreateLessonParams{
		OrgID: orgID, CourseID: courseID, ModuleID: moduleID, Title: title,
		Summary: trimOrNil(deref(req.Summary)), Kind: kind,
		Position: position, EstimatedMinutes: req.EstimatedMinutes,
	})
	if err != nil {
		httpx.Fail(w, statusOf(err, "lesson"))
		return
	}
	httpx.Created(w, lesson)
}

func (s *Server) getLesson(w http.ResponseWriter, r *http.Request) {
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
	lesson, err := s.db.LessonByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "lesson"))
		return
	}
	httpx.JSON(w, lesson)
}

type updateLessonRequest struct {
	Title            *string `json:"title"`
	Summary          *string `json:"summary"`
	Kind             *string `json:"kind"`
	Position         *int    `json:"position"`
	EstimatedMinutes *int    `json:"estimated_minutes"`
}

func (s *Server) updateLesson(w http.ResponseWriter, r *http.Request) {
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
	var req updateLessonRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// A PATCH must distinguish three cases per field: absent (leave alone),
	// present with a value (set it), and present but emptied (clear it).
	// A nil pointer already means the first, so the optional string fields
	// are only built when the client actually sent the key.
	p := store.UpdateLessonParams{
		Position: req.Position, EstimatedMinutes: req.EstimatedMinutes,
	}
	if req.Title != nil {
		p.Title = trimOrNil(*req.Title)
		if p.Title == nil {
			httpx.Fail(w, httpx.FieldError("title", "A lesson needs a title."))
			return
		}
	}
	if req.Summary != nil {
		// Keep the pointer non-nil even when the text is empty: nil means
		// "leave alone", and "" means "clear this field".
		v := strings.TrimSpace(*req.Summary)
		p.Summary = &v
	}
	if req.Kind != nil {
		kind := domain.LessonKind(*req.Kind)
		switch kind {
		case domain.LessonContent, domain.LessonAssessment, domain.LessonMixed:
			p.Kind = &kind
		default:
			httpx.Fail(w, httpx.FieldError("kind",
				"Lesson kind must be content, assessment or mixed."))
			return
		}
	}
	lesson, err := s.db.UpdateLesson(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "lesson"))
		return
	}
	httpx.JSON(w, lesson)
}

func (s *Server) deleteLesson(w http.ResponseWriter, r *http.Request) {
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
	if err := s.db.DeleteLesson(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "lesson"))
		return
	}
	httpx.NoContent(w)
}

type reorderRequest struct {
	OrderedIDs []string `json:"ordered_ids"`
}

// reorderModules persists the drag order of the course builder's top level
// list. The course id comes from the path, so the handler can only ever
// reorder modules inside the caller's own organisation.
func (s *Server) reorderModules(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	courseID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ids, err := decodeOrderedIDs(w, r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.db.ReorderModules(r.Context(), orgID, courseID, ids); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Server) reorderLessons(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	moduleID, err := pathUUID(r, "moduleId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ids, err := decodeOrderedIDs(w, r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.db.ReorderLessons(r.Context(), orgID, moduleID, ids); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Server) reorderBlocks(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ids, err := decodeOrderedIDs(w, r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.db.ReorderBlocks(r.Context(), orgID, lessonID, ids); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Server) reorderQuestions(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	courseID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ids, err := decodeOrderedIDs(w, r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.db.ReorderQuestions(r.Context(), orgID, courseID, ids); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func decodeOrderedIDs(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, error) {
	var req reorderRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return nil, err
	}
	if len(req.OrderedIDs) == 0 {
		return nil, ValidationError("Nothing to reorder.", map[string]any{
			"ordered_ids": "Send the full list of ids in their new order.",
		})
	}
	out := make([]uuid.UUID, 0, len(req.OrderedIDs))
	for _, raw := range req.OrderedIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, httpx.FieldError("ordered_ids", "One of the ids is not a valid UUID.")
		}
		out = append(out, id)
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Content blocks
// ---------------------------------------------------------------------

func (s *Server) listBlocks(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	blocks, err := s.db.ListBlocks(r.Context(), orgID, lessonID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "lesson"))
		return
	}
	if blocks == nil {
		blocks = []domain.Block{}
	}
	httpx.JSON(w, blocks)
}

type createBlockRequest struct {
	Type        string  `json:"type"`
	Title       *string `json:"title"`
	Body        *string `json:"body"`
	AssetID     *string `json:"asset_id"`
	PageFrom    *int    `json:"page_from"`
	PageTo      *int    `json:"page_to"`
	ChapterFrom *int    `json:"chapter_from"`
	ChapterTo   *int    `json:"chapter_to"`
}

// paginated reports whether a block type carries the extracted page and
// chapter data, so the page range is only required for those.
func paginated(t domain.BlockType) bool {
	return t == domain.BlockPDF || t == domain.BlockPPTX
}

func (s *Server) createBlock(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lessonID, err := pathUUID(r, "id")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req createBlockRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	btype := domain.BlockType(req.Type)
	switch btype {
	case domain.BlockText, domain.BlockVideo, domain.BlockImage,
		domain.BlockPDF, domain.BlockPPTX, domain.BlockFile, domain.BlockEmbed, domain.BlockQuiz:
	default:
		httpx.Fail(w, httpx.FieldError("type",
			"Block type must be text, video, image, pdf, pptx, file, embed or quiz."))
		return
	}
	fields := map[string]any{}
	p := store.CreateBlockParams{
		OrgID: orgID, LessonID: lessonID, Type: btype,
		Title: trimOrNil(deref(req.Title)), Body: trimOrNil(deref(req.Body)),
		PageFrom: req.PageFrom, PageTo: req.PageTo,
		ChapterFrom: req.ChapterFrom, ChapterTo: req.ChapterTo,
	}
	switch {
	case btype == domain.BlockText:
		if p.Body == nil {
			fields["body"] = "A text block needs some content."
		}
	case btype == domain.BlockVideo, btype == domain.BlockEmbed:
		// A video or embed block carries its URL in the body, which keeps
		// one column rather than a nullable one per type.
		if p.Body == nil {
			fields["body"] = "Paste the video or embed URL."
		}
	case btype == domain.BlockPDF || btype == domain.BlockPPTX || btype == domain.BlockImage:
		raw := trimOrNil(deref(req.AssetID))
		if raw == nil {
			fields["asset_id"] = "Upload the file first, then attach it here."
		} else if aid, err := uuid.Parse(*raw); err != nil {
			fields["asset_id"] = "That is not a valid asset id."
		} else {
			p.AssetID = &aid
		}
		// A paginated block without a range would render every page of a
		// long document into one lesson.
		if paginated(btype) && (p.PageFrom == nil || p.PageTo == nil) {
			fields["page_from"] = "Set the first and last page of this section."
		}
	}
	// A reversed range would render nothing at all.
	if p.PageFrom != nil && p.PageTo != nil && *p.PageTo < *p.PageFrom {
		fields["page_to"] = "The last page cannot be before the first page."
	}
	if len(fields) > 0 {
		httpx.Fail(w, ValidationError("Please correct the highlighted fields.", fields))
		return
	}
	block, err := s.db.CreateBlock(r.Context(), p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "block"))
		return
	}
	httpx.Created(w, block)
}

type updateBlockRequest struct {
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	AssetID    *string `json:"asset_id"`
	ClearAsset bool    `json:"clear_asset"`
	PageFrom   *int    `json:"page_from"`
	PageTo     *int    `json:"page_to"`
	Position   *int    `json:"position"`
}

func (s *Server) updateBlock(w http.ResponseWriter, r *http.Request) {
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
	var req updateBlockRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p := store.UpdateBlockParams{
		Title: trimOrNil(deref(req.Title)), Body: trimOrNil(deref(req.Body)),
		ClearAsset: req.ClearAsset,
		PageFrom:   req.PageFrom, PageTo: req.PageTo, Position: req.Position,
	}
	if raw := trimOrNil(deref(req.AssetID)); raw != nil {
		aid, err := uuid.Parse(*raw)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("asset_id", "That is not a valid asset id."))
			return
		}
		p.AssetID = &aid
	}
	block, err := s.db.UpdateBlock(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "block"))
		return
	}
	httpx.JSON(w, block)
}

func (s *Server) deleteBlock(w http.ResponseWriter, r *http.Request) {
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
	if err := s.db.DeleteBlock(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "block"))
		return
	}
	httpx.NoContent(w)
}

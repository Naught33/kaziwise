package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/material"
	"github.com/kaziwise/kaziwise_backend/internal/storage"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// Assets (screen 04)
// ---------------------------------------------------------------------

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	assets, total, err := s.db.ListAssets(r.Context(), orgID, store.AssetListFilter{
		Search: p.Search, Kind: p.RawQuery["kind"], Status: p.Status,
		Page: p.Page, PerPage: p.PerPage,
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, assets, p.Meta(total))
}

// uploadAsset accepts a PDF/PPTX, stores it in the configured driver and
// records the page and chapter counts parsed from it. A file that cannot be
// parsed is rejected with the parser's own message, so an administrator can
// fix the document and upload it again.
func (s *Server) uploadAsset(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if r.ContentLength > s.cfg.MaxUploadMB<<20 {
		httpx.Fail(w, httpx.TooLarge(
			fmt.Sprintf("Files are limited to %d MB.", s.cfg.MaxUploadMB)))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (s.cfg.MaxUploadMB+1)<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, httpx.FieldError("file",
			"Send the document as multipart/form-data with a 'file' field."))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest("upload_unreadable", "The upload could not be read."))
		return
	}
	if len(data) == 0 {
		httpx.Fail(w, httpx.FieldError("file", "The uploaded file is empty."))
		return
	}
	if int64(len(data)) > s.cfg.MaxUploadMB<<20 {
		httpx.Fail(w, httpx.TooLarge(
			fmt.Sprintf("Files are limited to %d MB.", s.cfg.MaxUploadMB)))
		return
	}

	// Sniff the content rather than trusting the extension, because a
	// renamed executable must not be stored as course material.
	kind := material.SniffKind(header.Filename, header.Header.Get("Content-Type"), data)
	if !kind.IsPaginated() {
		httpx.Fail(w, httpx.FieldError("file",
			"Only PDF and PPTX documents are supported for course content."))
		return
	}

	// Parse before storing so a corrupt file is rejected while the user is
	// still on the upload screen rather than after the assignment exists.
	result, err := material.Parse(kind, data, material.Options{})
	if err != nil {
		httpx.Fail(w, ValidationError("The document could not be read.", map[string]any{
			"file": err.Error(),
		}))
		return
	}
	pages := make([]domain.AssetPage, 0, len(result.Pages))
	for _, p := range result.Pages {
		pages = append(pages, domain.AssetPage{
			PageNumber: p.Number, TextContent: trimOrNil(p.Text), IsBlank: p.IsBlank,
			ChapterIndex: p.ChapterIndex, ChapterTitle: trimOrNil(p.ChapterTitle),
		})
	}

	key := storage.BuildKey(orgID.String(), string(kind), header.Filename)
	bucket := s.cfg.StorageBucket.Course
	saved, err := s.storage.Put(r.Context(), bucket, key,
		header.Header.Get("Content-Type"), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		httpx.Fail(w, httpx.NewError(http.StatusBadGateway, "storage_failed",
			"The file could not be saved: "+err.Error()))
		return
	}

	actor := actorID
	asset, err := s.db.CreateAsset(r.Context(), store.CreateAssetParams{
		OrgID: orgID, UploadedBy: &actor, Kind: domain.AssetKind(kind),
		OriginalName: header.Filename, StoragePath: saved.Path, Bucket: saved.Bucket,
		MimeType:  trimOrNil(header.Header.Get("Content-Type")),
		SizeBytes: int64(len(data)), PageCount: result.PageCount,
		ChapterCount: len(result.Chapters), Status: domain.AssetReady,
	})
	if err != nil {
		// Do not leave an orphaned object behind when the row fails.
		_ = s.storage.Delete(r.Context(), saved.Bucket, saved.Path)
		httpx.Fail(w, statusOf(err, "asset"))
		return
	}
	if len(pages) > 0 {
		if err := s.db.ReplaceAssetPages(r.Context(), orgID, asset.ID, pages,
			result.PageCount, len(result.Chapters)); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	s.audit(r, "asset.upload", "asset", asset.ID, map[string]any{
		"name": header.Filename, "kind": string(kind), "pages": result.PageCount,
		"chapters": len(result.Chapters),
	})
	if len(result.Warnings) > 0 {
		s.log.Warn("asset parsed with warnings",
			"asset_id", asset.ID, "warnings", result.Warnings)
	}
	httpx.Created(w, asset)
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
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
	asset, err := s.db.AssetByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "asset"))
		return
	}
	httpx.JSON(w, asset)
}

// assetPages returns the extracted text per page, used by the reader and
// the search index.
func (s *Server) assetPages(w http.ResponseWriter, r *http.Request) {
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
	if _, err := s.db.AssetByID(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "asset"))
		return
	}
	pages, err := s.db.AssetPages(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if pages == nil {
		pages = []domain.AssetPage{}
	}
	httpx.JSONMeta(w, pages, &httpx.Meta{Total: len(pages)})
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
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
	asset, err := s.db.DeleteAsset(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "asset"))
		return
	}
	// The row is gone; a failed object delete leaves an orphan in the
	// bucket but must not fail the response.
	_ = s.storage.Delete(r.Context(), asset.Bucket, asset.StoragePath)
	s.audit(r, "asset.delete", "asset", id, map[string]any{"name": asset.OriginalName})
	httpx.NoContent(w)
}

// ---------------------------------------------------------------------
// Questions / question bank (screen 06)
// ---------------------------------------------------------------------

func (s *Server) listQuestions(w http.ResponseWriter, r *http.Request) {
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
	questions, err := s.db.QuestionsForCourse(r.Context(), orgID, courseID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	if questions == nil {
		questions = []domain.Question{}
	}
	httpx.JSONMeta(w, questions, &httpx.Meta{Total: len(questions)})
}

type optionRequest struct {
	Label     string `json:"label"`
	IsCorrect bool   `json:"is_correct"`
}

type createQuestionRequest struct {
	LessonID    *string         `json:"lesson_id"`
	Type        string          `json:"type"`
	Prompt      string          `json:"prompt"`
	Hint        *string         `json:"hint"`
	Explanation *string         `json:"explanation"`
	Points      float64         `json:"points"`
	MinLength   *int            `json:"min_length"`
	MaxLength   *int            `json:"max_length"`
	IsRequired  *bool           `json:"is_required"`
	Options     []optionRequest `json:"options"`
	Position    *int            `json:"position"`
}

func (s *Server) createQuestion(w http.ResponseWriter, r *http.Request) {
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
	var req createQuestionRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// Verify the course exists in this org before writing a child row.
	if _, err := s.db.CourseByID(r.Context(), orgID, courseID); err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	qtype := domain.QuestionType(req.Type)
	fields := map[string]any{}
	if err := validateQuestionType(qtype); err != nil {
		fields["type"] = err.Error()
	}
	if strings.TrimSpace(req.Prompt) == "" {
		fields["prompt"] = "Enter the question text."
	}
	if req.Points < 0 {
		fields["points"] = "Points cannot be negative."
	}
	opts, err := validateOptions(qtype, req.Options)
	if err != nil {
		fields["options"] = err.Error()
	}
	if fields == nil {
		if err := validateTextLimits(qtype, req.MinLength, req.MaxLength); err != nil {
			fields["min_length"] = err.Error()
		}
	}
	if len(fields) > 0 {
		httpx.Fail(w, ValidationError("Please correct the highlighted fields.", fields))
		return
	}

	p := store.CreateQuestionParams{
		CourseID: courseID, OrgID: orgID, Type: qtype,
		Prompt: strings.TrimSpace(req.Prompt), Hint: trimOrNil(deref(req.Hint)),
		Explanation: trimOrNil(deref(req.Explanation)), Points: req.Points,
		MinLength: req.MinLength, MaxLength: req.MaxLength, Options: opts,
	}
	if req.IsRequired != nil {
		p.IsRequired = *req.IsRequired
	}
	if req.Position != nil {
		p.Position = *req.Position
	}
	if raw := trimOrNil(deref(req.LessonID)); raw != nil {
		lid, err := uuid.Parse(*raw)
		if err != nil {
			httpx.Fail(w, httpx.FieldError("lesson_id", "That is not a valid lesson id."))
			return
		}
		// The lesson must belong to this course, otherwise the question
		// would be invisible to learners but still countable in the bank.
		if _, err := s.db.LessonByID(r.Context(), orgID, lid); err != nil {
			httpx.Fail(w, httpx.FieldError("lesson_id",
				"That lesson does not exist in this organisation."))
			return
		}
		p.LessonID = &lid
	}
	question, err := s.db.CreateQuestion(r.Context(), p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "question"))
		return
	}
	httpx.Created(w, question)
}

type updateQuestionRequest struct {
	Prompt      *string          `json:"prompt"`
	Hint        *string          `json:"hint"`
	Explanation *string          `json:"explanation"`
	Points      *float64         `json:"points"`
	MinLength   *int             `json:"min_length"`
	MaxLength   *int             `json:"max_length"`
	IsRequired  *bool            `json:"is_required"`
	Position    *int             `json:"position"`
	Options     *[]optionRequest `json:"options"`
}

func (s *Server) updateQuestion(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "questionId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	existing, err := s.db.QuestionByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "question"))
		return
	}
	var req updateQuestionRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p := store.UpdateQuestionParams{
		Prompt: trimOrNil(deref(req.Prompt)), Hint: trimOrNil(deref(req.Hint)),
		Explanation: trimOrNil(deref(req.Explanation)), Points: req.Points,
		MinLength: req.MinLength, MaxLength: req.MaxLength,
		IsRequired: req.IsRequired, Position: req.Position,
	}
	// Options are only meaningful for the choice types.
	if req.Options != nil && (existing.Type == domain.QSingleChoice || existing.Type == domain.QMultiChoice) {
		opts, err := validateOptions(existing.Type, *req.Options)
		if err != nil {
			httpx.Fail(w, ValidationError("Please correct the highlighted fields.",
				map[string]any{"options": err.Error()}))
			return
		}
		p.Options = &opts
	}
	question, err := s.db.UpdateQuestion(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "question"))
		return
	}
	httpx.JSON(w, question)
}

func (s *Server) deleteQuestion(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	id, err := pathUUID(r, "questionId")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.db.DeleteQuestion(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "question"))
		return
	}
	httpx.NoContent(w)
}

func validateQuestionType(t domain.QuestionType) error {
	switch t {
	case domain.QSingleChoice, domain.QMultiChoice, domain.QShortText, domain.QLongText:
		return nil
	}
	return fmt.Errorf("type must be one of single_choice, multi_choice, short_text, long_text")
}

// validateOptions enforces the option rules per question type: a choice
// question needs at least two options and exactly one correct answer for
// single choice, and at least one correct answer for multi choice. Text
// questions must not carry options.
func validateOptions(t domain.QuestionType, in []optionRequest) ([]store.CreateOptionParams, error) {
	isChoice := t == domain.QSingleChoice || t == domain.QMultiChoice
	if !isChoice {
		if len(in) > 0 {
			return nil, fmt.Errorf("written questions must not define options")
		}
		return nil, nil
	}
	if len(in) < 2 {
		return nil, fmt.Errorf("a choice question needs at least two options")
	}
	out := make([]store.CreateOptionParams, 0, len(in))
	correct := 0
	seen := map[string]bool{}
	for _, o := range in {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			return nil, fmt.Errorf("every option needs a label")
		}
		key := strings.ToLower(label)
		if seen[key] {
			return nil, fmt.Errorf("option labels must be unique")
		}
		seen[key] = true
		if o.IsCorrect {
			correct++
		}
		out = append(out, store.CreateOptionParams{Label: label, IsCorrect: o.IsCorrect})
	}
	switch t {
	case domain.QSingleChoice:
		if correct != 1 {
			return nil, fmt.Errorf("a single choice question needs exactly one correct option")
		}
	case domain.QMultiChoice:
		if correct < 1 {
			return nil, fmt.Errorf("a multiple choice question needs at least one correct option")
		}
		if correct == len(in) {
			return nil, fmt.Errorf("marking every option correct makes the question meaningless")
		}
	}
	return out, nil
}

func validateTextLimits(t domain.QuestionType, minLen, maxLen *int) error {
	if t != domain.QShortText && t != domain.QLongText {
		if minLen != nil || maxLen != nil {
			return fmt.Errorf("length limits only apply to written questions")
		}
		return nil
	}
	if minLen != nil && *minLen < 0 {
		return fmt.Errorf("minimum length cannot be negative")
	}
	if maxLen != nil && *maxLen <= 0 {
		return fmt.Errorf("maximum length must be greater than zero")
	}
	if minLen != nil && maxLen != nil && *minLen > *maxLen {
		return fmt.Errorf("minimum length cannot exceed the maximum")
	}
	return nil
}

func (s *Server) listLessonQuestions(w http.ResponseWriter, r *http.Request) {
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
	questions, err := s.db.QuestionsForLesson(r.Context(), orgID, lessonID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "lesson"))
		return
	}
	if questions == nil {
		questions = []domain.Question{}
	}
	httpx.JSONMeta(w, questions, &httpx.Meta{Total: len(questions)})
}

// ---------------------------------------------------------------------
// Campaigns / assignments (screen 05)
// ---------------------------------------------------------------------

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.CampaignListFilter{
		Search: p.Search, Status: p.Status, Page: p.Page, PerPage: p.PerPage,
	}
	if raw := p.RawQuery["course_id"]; raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.CourseID = &id
		}
	}
	campaigns, total, err := s.db.ListCampaigns(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, campaigns, p.Meta(total))
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
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
	campaign, err := s.db.CampaignByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	detail := domain.CampaignDetail{Learners: []domain.Assignment{}}
	if assignments, _, err := s.db.ListAssignments(r.Context(), orgID,
		store.AssignmentFilter{CampaignID: &id, PerPage: 500}); err == nil {
		detail.Learners = assignments
	}
	httpx.JSON(w, map[string]any{"campaign": campaign, "learners": detail.Learners})
}

type createCampaignRequest struct {
	CourseID         string   `json:"course_id"`
	Name             string   `json:"name"`
	Description      *string  `json:"description"`
	AudienceType     string   `json:"audience_type"`
	AudienceDept     *string  `json:"audience_department"`
	LearnerIDs       []string `json:"learner_ids"`
	DueDate          *string  `json:"due_date"`
	PassMark         *float64 `json:"pass_mark"`
	IssueCertificate *bool    `json:"issue_certificate"`
	MaxAttempts      *int     `json:"max_attempts"`
	RequireLearning  *bool    `json:"require_learning_completion"`
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req createCampaignRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	fields := map[string]any{}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		fields["name"] = "Give the campaign a name."
	}
	courseID, err := uuid.Parse(strings.TrimSpace(req.CourseID))
	if err != nil {
		fields["course_id"] = "Choose the course this campaign assigns."
	}
	audience := domain.AudienceType(req.AudienceType)
	switch audience {
	case domain.AudienceAll, domain.AudienceDepartment, domain.AudienceUsers:
	case "":
		audience = domain.AudienceAll
	default:
		fields["audience_type"] = "Audience must be all, department or users."
	}
	if audience == domain.AudienceDepartment &&
		trimOrNil(deref(req.AudienceDept)) == nil {
		fields["audience_department"] = "Choose the department to assign to."
	}
	if audience == domain.AudienceUsers && len(req.LearnerIDs) == 0 {
		fields["learner_ids"] = "Pick at least one learner, or choose 'all'."
	}
	if req.PassMark != nil && (*req.PassMark < 0 || *req.PassMark > 100) {
		fields["pass_mark"] = "The pass mark must be between 0 and 100."
	}
	if req.MaxAttempts != nil && *req.MaxAttempts < 1 {
		fields["max_attempts"] = "Allow at least one attempt."
	}
	if raw := trimOrNil(deref(req.DueDate)); raw != nil {
		if _, err := time.Parse(time.RFC3339, *raw); err != nil {
			fields["due_date"] = "Use an RFC 3339 timestamp, for example 2026-01-31T17:00:00Z."
		}
	}
	if len(fields) > 0 {
		httpx.Fail(w, ValidationError("Please correct the highlighted fields.", fields))
		return
	}

	// A campaign can only assign a published course; assigning a draft
	// would leave learners with nothing to open.
	course, err := s.db.CourseByID(r.Context(), orgID, courseID)
	if err != nil {
		httpx.Fail(w, statusOf(err, "course"))
		return
	}
	if course.Status != domain.CoursePublished {
		httpx.Fail(w, httpx.Conflict("course_not_published",
			"Publish the course before assigning it."))
		return
	}
	// Referential integrity matters here: learner_ids and due_date feed
	// straight into SQL, so both are parsed before they reach the store.
	learnerIDs, err := s.resolveLearnerIDs(r, orgID, req.LearnerIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := store.CreateCampaignParams{
		OrgID: orgID, CourseID: courseID, Name: name,
		Description:  trimOrNil(deref(req.Description)),
		AudienceType: audience, AudienceDept: trimOrNil(deref(req.AudienceDept)),
		DueDate: trimOrNil(deref(req.DueDate)), CreatedBy: &actorID,
		LearnerIDs: learnerIDs,
	}
	if req.PassMark != nil {
		p.PassMark = *req.PassMark
	}
	if req.MaxAttempts != nil {
		p.MaxAttempts = *req.MaxAttempts
	}
	if req.IssueCertificate != nil {
		p.IssueCertificate = *req.IssueCertificate
	}
	if req.RequireLearning != nil {
		p.RequireLearning = *req.RequireLearning
	}
	campaign, err := s.db.CreateCampaign(r.Context(), p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	// The audience count is the single most useful thing to show next, so
	// it is resolved before responding.
	preview, _ := s.db.CampaignPreviewAudience(r.Context(), orgID, campaign.ID)
	s.audit(r, "campaign.create", "campaign", campaign.ID, map[string]any{
		"course_id": courseID, "audience": string(audience), "preview": preview,
	})
	httpx.Created(w, map[string]any{"campaign": campaign, "audience_count": preview})
}

func (s *Server) updateCampaign(w http.ResponseWriter, r *http.Request) {
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
	var req createCampaignRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// An active campaign is already tracking learners; changing who it
	// applies to mid-flight would rewrite history.
	current, err := s.db.CampaignByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	locked := current.Status != domain.CampaignDraft
	p := store.UpdateCampaignParams{
		Name:        trimOrNil(req.Name),
		Description: trimOrNil(deref(req.Description)),
		PassMark:    req.PassMark, MaxAttempts: req.MaxAttempts,
		IssueCertificate: req.IssueCertificate, RequireLearning: req.RequireLearning,
	}
	if !locked {
		if req.AudienceType != "" {
			audience := domain.AudienceType(req.AudienceType)
			switch audience {
			case domain.AudienceAll, domain.AudienceDepartment, domain.AudienceUsers:
				p.AudienceType = &audience
			default:
				httpx.Fail(w, httpx.FieldError("audience_type",
					"Audience must be all, department or users."))
				return
			}
		}
		p.AudienceDept = trimOrNil(deref(req.AudienceDept))
		if raw := trimOrNil(deref(req.DueDate)); raw != nil {
			if _, err := time.Parse(time.RFC3339, *raw); err != nil {
				httpx.Fail(w, httpx.FieldError("due_date",
					"Use an RFC 3339 timestamp."))
				return
			}
			p.DueDate = raw
		}
		if len(req.LearnerIDs) > 0 {
			learnerIDs, err := s.resolveLearnerIDs(r, orgID, req.LearnerIDs)
			if err != nil {
				httpx.Fail(w, err)
				return
			}
			p.LearnerIDs = &learnerIDs
		}
	} else if req.AudienceType != "" {
		httpx.Fail(w, httpx.Conflict("campaign_locked",
			"This campaign has been launched. Close it before changing its audience."))
		return
	}
	campaign, err := s.db.UpdateCampaign(r.Context(), orgID, id, p)
	if err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	s.audit(r, "campaign.update", "campaign", id, nil)
	httpx.JSON(w, campaign)
}

func (s *Server) launchCampaign(w http.ResponseWriter, r *http.Request) {
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
	result, err := s.svc.LaunchCampaign(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "campaign.launch", "campaign", id, map[string]any{
		"assigned": result.Assigned, "skipped": result.Skipped,
	})
	httpx.JSON(w, result)
}

func (s *Server) closeCampaign(w http.ResponseWriter, r *http.Request) {
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
	campaign, err := s.svc.CloseCampaign(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "campaign.close", "campaign", id, nil)
	httpx.JSON(w, campaign)
}

func (s *Server) deleteCampaign(w http.ResponseWriter, r *http.Request) {
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
	campaign, err := s.db.CampaignByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	// A launched campaign is the record of what a learner was asked to do,
	// so it may only be removed while still a draft.
	if campaign.Status != domain.CampaignDraft {
		httpx.Fail(w, httpx.Conflict("campaign_locked",
			"Only a draft campaign can be deleted. Close it instead."))
		return
	}
	if err := s.db.DeleteCampaign(r.Context(), orgID, id); err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	s.audit(r, "campaign.delete", "campaign", id, nil)
	httpx.NoContent(w)
}

// campaignAudience reports who a campaign currently reaches, without
// creating anything.
func (s *Server) campaignAudience(w http.ResponseWriter, r *http.Request) {
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
	learners, err := s.db.CampaignAudience(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "campaign"))
		return
	}
	// Only the count and ids are exposed: the list screen needs to know
	// who is in scope, not their full records.
	ids := make([]uuid.UUID, 0, len(learners))
	ids = append(ids, learners...)
	httpx.JSONMeta(w, map[string]any{"learner_ids": ids},
		&httpx.Meta{Total: len(ids)})
}

// resolveLearnerIDs validates that every id belongs to this org.
func (s *Server) resolveLearnerIDs(r *http.Request, orgID uuid.UUID, raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, item := range raw {
		id, err := uuid.Parse(strings.TrimSpace(item))
		if err != nil {
			return nil, httpx.FieldError("learner_ids", "One of the learner ids is not a valid UUID.")
		}
		user, err := s.db.UserByID(r.Context(), orgID, id)
		if err != nil {
			return nil, httpx.FieldError("learner_ids",
				"One of the selected learners does not exist in this organisation.")
		}
		if user.Role != domain.RoleLearner {
			return nil, httpx.FieldError("learner_ids",
				"Only learners can be assigned training.")
		}
		out = append(out, id)
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Assignment tracking
// ---------------------------------------------------------------------

func (s *Server) listAssignments(w http.ResponseWriter, r *http.Request) {
	orgID, actorID, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	f := store.AssignmentFilter{
		Status: p.Status, Search: p.Search, Page: p.Page, PerPage: p.PerPage,
		Department: p.RawQuery["department"],
		From:       trimOrNil(p.RawQuery["from"]),
		To:         trimOrNil(p.RawQuery["to"]),
	}
	if p.Overdue != nil {
		f.Overdue = *p.Overdue
	}
	// A learner only ever sees their own row; a manager sees their team.
	switch ClaimsFrom(r.Context()).Role {
	case auth.RoleLearner:
		f.LearnerID = &actorID
	case auth.RoleManager:
		f.ManagerID = &actorID
	}
	if raw := p.RawQuery["learner_id"]; raw != "" {
		if ClaimsFrom(r.Context()).Role == auth.RoleLearner {
			httpx.Fail(w, httpx.Forbidden("You can only view your own training."))
			return
		}
		if id, err := uuid.Parse(raw); err == nil {
			f.LearnerID = &id
		}
	}
	if raw := p.RawQuery["campaign_id"]; raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.CampaignID = &id
		}
	}
	if raw := p.RawQuery["course_id"]; raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.CourseID = &id
		}
	}
	assignments, total, err := s.db.ListAssignments(r.Context(), orgID, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, assignments, p.Meta(total))
}

func (s *Server) getAssignment(w http.ResponseWriter, r *http.Request) {
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
	assignment, err := s.db.AssignmentByID(r.Context(), orgID, id)
	if err != nil {
		httpx.Fail(w, statusOf(err, "assignment"))
		return
	}
	if ClaimsFrom(r.Context()).Role == auth.RoleLearner && assignment.LearnerID != actorID {
		httpx.Fail(w, httpx.Forbidden("You can only view your own training."))
		return
	}
	httpx.JSON(w, assignment)
}

func (s *Server) remindAssignment(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Note string `json:"note"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	reminder, err := s.svc.RemindAssignment(r.Context(), orgID, actorID, id, req.Note)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "assignment.remind", "assignment", id, nil)
	httpx.Created(w, reminder)
}

func (s *Server) remindCampaign(w http.ResponseWriter, r *http.Request) {
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
	result, err := s.svc.RemindCampaign(r.Context(), orgID, actorID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "campaign.remind", "campaign", id, map[string]any{
		"reminded": result.Reminded, "skipped": result.Skipped,
	})
	httpx.JSON(w, result)
}

func (s *Server) listReminders(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := orgAndActor(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p := httpx.Query(r)
	reminders, total, err := s.db.ListReminders(r.Context(), orgID, p.Page, p.PerPage)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSONMeta(w, reminders, p.Meta(total))
}

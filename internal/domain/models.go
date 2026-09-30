// Package domain holds the KaziWise business entities exactly as they
// cross the API boundary. JSON tags here are the contract the front end
// codes against; see README.md for the same shapes in documentation form.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------
// Roles and enums
// ---------------------------------------------------------------------

type Role string

const (
	RoleSuperAdmin Role = "super_admin" // manages the KaziWise platform
	RoleOrgAdmin   Role = "org_admin"   // manages employees, courses, campaigns, reports
	RoleManager    Role = "manager"     // sees the training status of their team
	RoleLearner    Role = "learner"     // completes assigned training
)

func (r Role) Valid() bool {
	switch r {
	case RoleSuperAdmin, RoleOrgAdmin, RoleManager, RoleLearner:
		return true
	}
	return false
}

func (r Role) IsAdmin() bool  { return r == RoleSuperAdmin || r == RoleOrgAdmin }
func (r Role) IsStaff() bool  { return r.IsAdmin() || r == RoleManager }
func (r Role) String() string { return string(r) }

type UserStatus string

const (
	UserActive   UserStatus = "active"
	UserInvited  UserStatus = "invited"
	UserInactive UserStatus = "inactive"
)

type CourseStatus string

const (
	CourseDraft     CourseStatus = "draft"
	CoursePublished CourseStatus = "published"
	CourseArchived  CourseStatus = "archived"
)

type LessonKind string

const (
	LessonContent    LessonKind = "content"
	LessonAssessment LessonKind = "assessment"
	LessonMixed      LessonKind = "mixed"
)

type BlockType string

const (
	BlockText  BlockType = "text"  // rich text / markdown
	BlockVideo BlockType = "video" // mp4 / webm / YouTube URL in Body
	BlockImage BlockType = "image"
	BlockPDF   BlockType = "pdf"   // paginated, chapters derived from blank pages
	BlockPPTX  BlockType = "pptx"  // paginated, chapters derived from blank slides
	BlockFile  BlockType = "file"  // any other download
	BlockEmbed BlockType = "embed" // iframe / external URL
	BlockQuiz  BlockType = "quiz"  // inline knowledge check
)

type AssetKind string

const (
	AssetPDF      AssetKind = "pdf"
	AssetPPTX     AssetKind = "pptx"
	AssetImage    AssetKind = "image"
	AssetVideo    AssetKind = "video"
	AssetDocument AssetKind = "document"
	AssetOther    AssetKind = "other"
)

type AssetStatus string

const (
	AssetUploaded   AssetStatus = "uploaded"
	AssetProcessing AssetStatus = "processing"
	AssetReady      AssetStatus = "ready"
	AssetFailed     AssetStatus = "failed"
)

// QuestionType enumerates the four supported assessment question types.
type QuestionType string

const (
	// One correct option, auto-graded.
	QSingleChoice QuestionType = "single_choice"
	// One or more correct options, auto-graded.
	QMultiChoice QuestionType = "multi_choice"
	// Typed sentence. min_length / max_length enforced. Manually graded.
	QShortText QuestionType = "short_text"
	// Typed paragraph(s). min_length / max_length enforced. Manually graded.
	QLongText QuestionType = "long_text"
)

func (q QuestionType) IsChoice() bool   { return q == QSingleChoice || q == QMultiChoice }
func (q QuestionType) IsText() bool     { return q == QShortText || q == QLongText }
func (q QuestionType) AutoGraded() bool { return q.IsChoice() }
func (q QuestionType) Valid() bool      { return q.IsChoice() || q.IsText() }

type CampaignStatus string

const (
	CampaignDraft  CampaignStatus = "draft"
	CampaignActive CampaignStatus = "active"
	CampaignClosed CampaignStatus = "closed"
)

type AudienceType string

const (
	AudienceAll        AudienceType = "all"
	AudienceDepartment AudienceType = "department"
	AudienceUsers      AudienceType = "users"
)

// AssignmentStatus is the learner tracking state machine.
type AssignmentStatus string

const (
	AssignNotStarted    AssignmentStatus = "not_started"
	AssignInProgress    AssignmentStatus = "in_progress"
	AssignPendingReview AssignmentStatus = "pending_review" // manual grading outstanding
	AssignPassed        AssignmentStatus = "passed"
	AssignFailed        AssignmentStatus = "failed"
	AssignOverdue       AssignmentStatus = "overdue"
)

// IsTerminal reports whether no further learner action changes the state.
func (s AssignmentStatus) IsTerminal() bool {
	return s == AssignPassed || s == AssignFailed
}

// IsTerminal reports whether an attempt has been finalised. An attempt is
// only final once a pass/fail outcome exists, so pending_review is not
// terminal even though it can no longer be edited by the learner.
func (s AttemptStatus) IsTerminal() bool {
	return s == AttemptPassed || s == AttemptFailed
}

type AttemptStatus string

const (
	AttemptInProgress    AttemptStatus = "in_progress"
	AttemptPendingReview AttemptStatus = "pending_review"
	AttemptPassed        AttemptStatus = "passed"
	AttemptFailed        AttemptStatus = "failed"
)

// ---------------------------------------------------------------------
// Organisation + people
// ---------------------------------------------------------------------

type Organisation struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Industry  *string   `json:"industry,omitempty"`
	Country   *string   `json:"country,omitempty"`
	Timezone  string    `json:"timezone"`
	IsDemo    bool      `json:"is_demo"`
	CreatedAt time.Time `json:"created_at"`
}

// User is a KaziWise profile. Authentication is handled by Supabase;
// this record owns identity, role and org membership.
type User struct {
	ID             uuid.UUID  `json:"id"`
	AuthUserID     *uuid.UUID `json:"auth_user_id,omitempty"`
	OrgID          uuid.UUID  `json:"org_id"`
	Email          string     `json:"email"`
	FullName       string     `json:"full_name"`
	Role           Role       `json:"role"`
	Department     *string    `json:"department,omitempty"`
	EmployeeNumber *string    `json:"employee_number,omitempty"`
	JobTitle       *string    `json:"job_title,omitempty"`
	Phone          *string    `json:"phone,omitempty"`
	Status         UserStatus `json:"status"`
	AvatarURL      *string    `json:"avatar_url,omitempty"`
	ManagerID      *uuid.UUID `json:"manager_id,omitempty"`
	ManagerName    *string    `json:"manager_name,omitempty"`
	MustReset      bool       `json:"must_reset_password"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Aggregates, populated on list/detail endpoints.
	AssignedCount  int     `json:"assigned_count,omitempty"`
	CompletedCount int     `json:"completed_count,omitempty"`
	PassedCount    int     `json:"passed_count,omitempty"`
	ProgressPct    float64 `json:"progress_percent,omitempty"`
}

// TeamMember is a learner as seen by a manager.
type TeamMember struct {
	User
	Status          AssignmentStatus `json:"assignment_status,omitempty"`
	CourseTitle     *string          `json:"course_title,omitempty"`
	ProgressPercent float64          `json:"progress_percent,omitempty"`
	FinalScore      *float64         `json:"final_score,omitempty"`
	DueDate         *time.Time       `json:"due_date,omitempty"`
	LastActivityAt  *time.Time       `json:"last_activity_at,omitempty"`
}

// ---------------------------------------------------------------------
// Assets (uploaded course material)
// ---------------------------------------------------------------------

type Asset struct {
	ID           uuid.UUID   `json:"id"`
	OrgID        uuid.UUID   `json:"org_id"`
	UploadedBy   *uuid.UUID  `json:"uploaded_by,omitempty"`
	Kind         AssetKind   `json:"kind"`
	Status       AssetStatus `json:"status"`
	OriginalName string      `json:"original_name"`
	StoragePath  string      `json:"storage_path"`
	Bucket       string      `json:"bucket"`
	MimeType     *string     `json:"mime_type,omitempty"`
	SizeBytes    int64       `json:"size_bytes"`
	Checksum     *string     `json:"checksum,omitempty"`
	PageCount    int         `json:"page_count"`
	ChapterCount int         `json:"chapter_count"`
	ParseError   *string     `json:"parse_error,omitempty"`
	DownloadURL  *string     `json:"download_url,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// AssetPage is one page/slide of a paginated asset.
// A blank page is a CHAPTER DELIMITER: it is stored, never rendered.
type AssetPage struct {
	ID           uuid.UUID `json:"id"`
	AssetID      uuid.UUID `json:"asset_id"`
	PageNumber   int       `json:"page_number"`
	IsBlank      bool      `json:"is_blank"`
	ChapterIndex int       `json:"chapter_index"`
	ChapterTitle *string   `json:"chapter_title,omitempty"`
	TextContent  *string   `json:"text_content,omitempty"`
}

// AssetWithPages is returned by the upload endpoint and the parse
// endpoint so the builder can show chapters without re-fetching.
type AssetWithPages struct {
	Asset
	Pages      []AssetPage      `json:"pages"`
	Renderable []RenderablePage `json:"renderable_pages"`
}

// RenderablePage is a non-blank page plus its viewing context. Blank
// pages are deliberately absent from this list.
type RenderablePage struct {
	PageNumber   int     `json:"page_number"`
	ChapterIndex int     `json:"chapter_index"`
	ChapterTitle *string `json:"chapter_title,omitempty"`
	TextContent  *string `json:"text_content,omitempty"`
	FileURL      string  `json:"file_url"`
	// The original 1-based page to show for a viewer that renders the
	// source document (pdf.js / LibreOffice export / image sequence).
	SourcePage int `json:"source_page"`
}

// ---------------------------------------------------------------------
// Course engine
// ---------------------------------------------------------------------

type Course struct {
	ID               uuid.UUID    `json:"id"`
	OrgID            uuid.UUID    `json:"org_id"`
	Title            string       `json:"title"`
	Code             *string      `json:"code,omitempty"`
	Description      *string      `json:"description,omitempty"`
	Category         *string      `json:"category,omitempty"`
	CoverAssetID     *uuid.UUID   `json:"cover_asset_id,omitempty"`
	CoverURL         *string      `json:"cover_url,omitempty"`
	Status           CourseStatus `json:"status"`
	EstimatedMinutes int          `json:"estimated_minutes"`
	CreatedBy        *uuid.UUID   `json:"created_by,omitempty"`
	PublishedAt      *time.Time   `json:"published_at,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`

	// Aggregates
	ModuleCount       int     `json:"module_count"`
	LessonCount       int     `json:"lesson_count"`
	BlockCount        int     `json:"block_count"`
	QuestionCount     int     `json:"question_count"`
	TotalPoints       float64 `json:"total_points"`
	DurationMinutes   int     `json:"duration_minutes"`
	AssignedLearners  int     `json:"assigned_learners,omitempty"`
	CompletedLearners int     `json:"completed_learners,omitempty"`
	CompletionPct     float64 `json:"completion_percent,omitempty"`
}

type Module struct {
	ID          uuid.UUID `json:"id"`
	CourseID    uuid.UUID `json:"course_id"`
	OrgID       uuid.UUID `json:"org_id"`
	Title       string    `json:"title"`
	Description *string   `json:"description,omitempty"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	Lessons         []Lesson `json:"lessons,omitempty"`
	LessonCount     int      `json:"lesson_count"`
	DurationMinutes int      `json:"duration_minutes"`
}

type Lesson struct {
	ID               uuid.UUID  `json:"id"`
	ModuleID         uuid.UUID  `json:"module_id"`
	CourseID         uuid.UUID  `json:"course_id"`
	OrgID            uuid.UUID  `json:"org_id"`
	Title            string     `json:"title"`
	Summary          *string    `json:"summary,omitempty"`
	Kind             LessonKind `json:"kind"`
	Position         int        `json:"position"`
	EstimatedMinutes int        `json:"estimated_minutes"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`

	Blocks        []Block `json:"blocks,omitempty"`
	BlockCount    int     `json:"block_count"`
	QuestionCount int     `json:"question_count"`
	// Learner-specific, only present on learner endpoints.
	Status      *string    `json:"status,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Block is one content element inside a lesson. For PDF/PPTX blocks the
// page range is stored and blank pages inside it are chapter delimiters
// that are skipped when rendering.
type Block struct {
	ID          uuid.UUID  `json:"id"`
	LessonID    uuid.UUID  `json:"lesson_id"`
	OrgID       uuid.UUID  `json:"org_id"`
	Type        BlockType  `json:"type"`
	Position    int        `json:"position"`
	Title       *string    `json:"title,omitempty"`
	Body        *string    `json:"body,omitempty"`
	AssetID     *uuid.UUID `json:"asset_id,omitempty"`
	PageFrom    *int       `json:"page_from,omitempty"`
	PageTo      *int       `json:"page_to,omitempty"`
	ChapterFrom *int       `json:"chapter_from,omitempty"`
	ChapterTo   *int       `json:"chapter_to,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Hydrated
	Asset        *Asset           `json:"asset,omitempty"`
	URL          *string          `json:"url,omitempty"`
	Pages        []RenderablePage `json:"pages,omitempty"`
	ChapterCount int              `json:"chapter_count,omitempty"`
	// SeenPages is the learner's consumed page numbers (learner view).
	SeenPages []int `json:"seen_pages,omitempty"`
}

// CourseOutline is the nested structure the Course Builder and the
// course player both consume.
type CourseOutline struct {
	Course  Course       `json:"course"`
	Modules []Module     `json:"modules"`
	Stats   OutlineStats `json:"stats"`
}

type OutlineStats struct {
	Modules         int     `json:"modules"`
	Lessons         int     `json:"lessons"`
	Blocks          int     `json:"blocks"`
	Questions       int     `json:"questions"`
	TotalPoints     float64 `json:"total_points"`
	DurationMinutes int     `json:"duration_minutes"`
	Chapters        int     `json:"chapters"`
}

// ---------------------------------------------------------------------
// Assessments
// ---------------------------------------------------------------------

type Option struct {
	ID         uuid.UUID `json:"id"`
	QuestionID uuid.UUID `json:"question_id"`
	Label      string    `json:"label"`
	IsCorrect  bool      `json:"is_correct,omitempty"` // omitted for learners
	Position   int       `json:"position"`
}

// Question is authored by an admin. is_correct / explanation are stripped
// from every response sent to a learner.
type Question struct {
	ID          uuid.UUID    `json:"id"`
	CourseID    uuid.UUID    `json:"course_id"`
	LessonID    *uuid.UUID   `json:"lesson_id,omitempty"`
	OrgID       uuid.UUID    `json:"org_id"`
	Type        QuestionType `json:"type"`
	Prompt      string       `json:"prompt"`
	Hint        *string      `json:"hint,omitempty"`
	Explanation *string      `json:"explanation,omitempty"`
	Points      float64      `json:"points"`
	Position    int          `json:"position"`
	MinLength   *int         `json:"min_length,omitempty"`
	MaxLength   *int         `json:"max_length,omitempty"`
	IsRequired  bool         `json:"is_required"`
	Options     []Option     `json:"options,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// WithoutAnswers returns a copy of the question with the answer key
// removed. Learner-facing endpoints must serialise this copy rather than
// the stored question: the omitempty tag on Option.IsCorrect means a false
// value is dropped along with a true one, so the copy cannot leak the key.
func (q Question) WithoutAnswers() Question {
	out := q
	out.Explanation = nil
	out.Options = make([]Option, 0, len(q.Options))
	for _, o := range q.Options {
		out.Options = append(out.Options, Option{
			ID: o.ID, QuestionID: o.QuestionID, Label: o.Label, Position: o.Position,
		})
	}
	return out
}

// QuestionPaper is the learner-safe assessment.
type QuestionPaper struct {
	AttemptID     uuid.UUID  `json:"attempt_id,omitempty"`
	AssignmentID  uuid.UUID  `json:"assignment_id"`
	CourseID      uuid.UUID  `json:"course_id"`
	CourseTitle   string     `json:"course_title"`
	PassMark      float64    `json:"pass_mark"`
	MaxAttempts   int        `json:"max_attempts"`
	AttemptsUsed  int        `json:"attempts_used"`
	TotalPoints   float64    `json:"total_points"`
	QuestionCount int        `json:"question_count"`
	DurationMins  int        `json:"duration_minutes,omitempty"`
	Questions     []Question `json:"questions"`
}

// ---------------------------------------------------------------------
// Campaigns
// ---------------------------------------------------------------------

type Campaign struct {
	ID               uuid.UUID      `json:"id"`
	OrgID            uuid.UUID      `json:"org_id"`
	CourseID         uuid.UUID      `json:"course_id"`
	CourseTitle      string         `json:"course_title,omitempty"`
	Name             string         `json:"name"`
	Description      *string        `json:"description,omitempty"`
	Status           CampaignStatus `json:"status"`
	AudienceType     AudienceType   `json:"audience_type"`
	AudienceDept     *string        `json:"audience_department,omitempty"`
	DueDate          *time.Time     `json:"due_date,omitempty"`
	PassMark         float64        `json:"pass_mark"`
	IssueCertificate bool           `json:"issue_certificate"`
	MaxAttempts      int            `json:"max_attempts"`
	RequireLearning  bool           `json:"require_learning"`
	CreatedBy        *uuid.UUID     `json:"created_by,omitempty"`
	LaunchedAt       *time.Time     `json:"launched_at,omitempty"`
	ClosedAt         *time.Time     `json:"closed_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`

	AudienceCount  int      `json:"audience_count"`
	AssignedCount  int      `json:"assigned_count"`
	StartedCount   int      `json:"started_count"`
	CompletedCount int      `json:"completed_count"`
	PassedCount    int      `json:"passed_count"`
	FailedCount    int      `json:"failed_count"`
	OverdueCount   int      `json:"overdue_count"`
	ProgressPct    float64  `json:"progress_percent"`
	AvgScore       *float64 `json:"average_score,omitempty"`
}

// CampaignDetail wraps a campaign with its resolved audience.
type CampaignDetail struct {
	Campaign
	Learners []Assignment `json:"learners"`
}

// ---------------------------------------------------------------------
// Learning
// ---------------------------------------------------------------------

// Assignment is a learner x campaign row: the tracking ledger.
type Assignment struct {
	ID             uuid.UUID        `json:"id"`
	OrgID          uuid.UUID        `json:"org_id"`
	CampaignID     uuid.UUID        `json:"campaign_id"`
	CampaignName   string           `json:"campaign_name,omitempty"`
	CourseID       uuid.UUID        `json:"course_id"`
	CourseTitle    string           `json:"course_title"`
	LearnerID      uuid.UUID        `json:"learner_id"`
	LearnerName    string           `json:"learner_name,omitempty"`
	LearnerEmail   string           `json:"learner_email,omitempty"`
	Department     *string          `json:"department,omitempty"`
	ManagerID      *uuid.UUID       `json:"manager_id,omitempty"`
	ManagerName    *string          `json:"manager_name,omitempty"`
	Status         AssignmentStatus `json:"status"`
	AssignedAt     time.Time        `json:"assigned_at"`
	DueDate        *time.Time       `json:"due_date,omitempty"`
	StartedAt      *time.Time       `json:"started_at,omitempty"`
	LastActivityAt time.Time        `json:"last_activity_at"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`
	LessonsTotal   int              `json:"lessons_total"`
	LessonsDone    int              `json:"lessons_done"`
	ProgressPct    float64          `json:"progress_percent"`
	AttemptsUsed   int              `json:"attempts_used"`
	BestScore      *float64         `json:"best_score,omitempty"`
	FinalScore     *float64         `json:"final_score,omitempty"`
	PassedAt       *time.Time       `json:"passed_at,omitempty"`
	IsOverdue      bool             `json:"is_overdue"`
	Certificates   int              `json:"certificates_issued"`
	OverdueSentAt  *time.Time       `json:"overdue_notified_at,omitempty"`
}

// LearnerDashboard is the screen-08 Learner View payload.
type LearnerDashboard struct {
	User             User          `json:"user"`
	OverallProgress  float64       `json:"overall_progress_percent"`
	AssignedCount    int           `json:"assigned_count"`
	InProgressCount  int           `json:"in_progress_count"`
	CompletedCount   int           `json:"completed_count"`
	PassedCount      int           `json:"passed_count"`
	FailedCount      int           `json:"failed_count"`
	OverdueCount     int           `json:"overdue_count"`
	RequiredTraining []Assignment  `json:"required_training"`
	Certificates     []Certificate `json:"certificates"`
	Greeting         string        `json:"greeting"`
}

type Attempt struct {
	ID            uuid.UUID     `json:"id"`
	AssignmentID  uuid.UUID     `json:"assignment_id"`
	LearnerID     uuid.UUID     `json:"learner_id"`
	AttemptNumber int           `json:"attempt_number"`
	Status        AttemptStatus `json:"status"`
	MaxScore      float64       `json:"max_score"`
	Score         float64       `json:"score"`
	Percent       float64       `json:"percent"`
	Passed        *bool         `json:"passed,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	SubmittedAt   *time.Time    `json:"submitted_at,omitempty"`
	GradedAt      *time.Time    `json:"graded_at,omitempty"`
	GradedByName  *string       `json:"graded_by,omitempty"`
	Answers       []Answer      `json:"answers,omitempty"`
	PendingManual int           `json:"pending_manual_grade_count"`
	PassMark      float64       `json:"pass_mark"`
	ReviewNeeded  bool          `json:"review_needed"`
}

// Answer is a learner's response to one question.
type Answer struct {
	ID                uuid.UUID   `json:"id"`
	AttemptID         uuid.UUID   `json:"attempt_id"`
	QuestionID        uuid.UUID   `json:"question_id"`
	ResponseText      *string     `json:"response_text,omitempty"`
	SelectedOptionIDs []uuid.UUID `json:"selected_option_ids"`
	IsCorrect         *bool       `json:"is_correct,omitempty"`
	PointsAwarded     *float64    `json:"points_awarded,omitempty"`
	AutoGraded        bool        `json:"auto_graded"`
	Feedback          *string     `json:"feedback,omitempty"`
	GradedByName      *string     `json:"graded_by,omitempty"`
	GradedAt          *time.Time  `json:"graded_at,omitempty"`
	// Hydrated for review screens
	Question *Question `json:"question,omitempty"`
	Options  []Option  `json:"options,omitempty"`
}

// ---------------------------------------------------------------------
// Certificates
// ---------------------------------------------------------------------

type Certificate struct {
	ID                uuid.UUID  `json:"id"`
	OrgID             uuid.UUID  `json:"org_id"`
	LearnerID         uuid.UUID  `json:"learner_id"`
	CourseID          uuid.UUID  `json:"course_id"`
	CampaignID        *uuid.UUID `json:"campaign_id,omitempty"`
	AttemptID         *uuid.UUID `json:"attempt_id,omitempty"`
	CertificateNumber string     `json:"certificate_number"`
	VerificationCode  string     `json:"verification_code"`
	LearnerName       string     `json:"learner_name"`
	CourseTitle       string     `json:"course_title"`
	Score             float64    `json:"score"`
	CompletedAt       time.Time  `json:"completed_at"`
	IssuedAt          time.Time  `json:"issued_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	RevokedReason     *string    `json:"revoked_reason,omitempty"`
	OrgName           string     `json:"org_name,omitempty"`
	ShareURL          string     `json:"share_url,omitempty"`
	RenderURL         string     `json:"render_url,omitempty"`
	Valid             bool       `json:"valid"`
}

// ---------------------------------------------------------------------
// Reporting
// ---------------------------------------------------------------------

type DashboardKPIs struct {
	Employees          int      `json:"employees"`
	ActiveEmployees    int      `json:"active_employees"`
	Departments        int      `json:"departments"`
	TotalCourses       int      `json:"courses"`
	PublishedCourses   int      `json:"published_courses"`
	DraftCourses       int      `json:"draft_courses"`
	ActiveCampaigns    int      `json:"active_campaigns"`
	Assigned           int      `json:"assigned"`
	Started            int      `json:"started"`
	Completed          int      `json:"completed"`
	Passed             int      `json:"passed"`
	Failed             int      `json:"failed"`
	Overdue            int      `json:"overdue"`
	PendingReview      int      `json:"pending_review"`
	CompletionPct      float64  `json:"completion_percent"`
	PassRatePct        float64  `json:"pass_rate_percent"`
	CertificatesIssued int      `json:"certificates_issued"`
	AvgScore           *float64 `json:"average_score,omitempty"`
}

// AttentionItem is one row of the dashboard "Attention Required" list.
type AttentionItem struct {
	Type        string     `json:"type"` // overdue | failed | due_soon | pending_review
	Severity    string     `json:"severity"`
	LearnerID   uuid.UUID  `json:"learner_id"`
	LearnerName string     `json:"learner_name"`
	CourseTitle string     `json:"course_title"`
	Detail      string     `json:"detail"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	URL         string     `json:"url,omitempty"`
}

type Dashboard struct {
	KPIs               DashboardKPIs        `json:"kpis"`
	ActiveCampaigns    []Campaign           `json:"active_campaigns"`
	Attention          []AttentionItem      `json:"attention_required"`
	DepartmentProgress []DepartmentProgress `json:"department_progress"`
	RecentCertificates []Certificate        `json:"recent_certificates"`
	GeneratedAt        time.Time            `json:"generated_at"`
}

type DepartmentProgress struct {
	Department    string  `json:"department"`
	Headcount     int     `json:"headcount"`
	Assigned      int     `json:"assigned"`
	Completed     int     `json:"completed"`
	Passed        int     `json:"passed"`
	Overdue       int     `json:"overdue"`
	CompletionPct float64 `json:"completion_percent"`
	PassRatePct   float64 `json:"pass_rate_percent"`
}

// StatusBreakdown powers the reports summary.
type StatusBreakdown struct {
	NotStarted    int     `json:"not_started"`
	InProgress    int     `json:"in_progress"`
	PendingReview int     `json:"pending_review"`
	Passed        int     `json:"passed"`
	Failed        int     `json:"failed"`
	Overdue       int     `json:"overdue"`
	Total         int     `json:"total"`
	PassRatePct   float64 `json:"pass_rate_percent"`
	AvgScorePct   float64 `json:"average_score_percent"`
}

type ReportSummary struct {
	Breakdown    StatusBreakdown      `json:"breakdown"`
	Rows         []Assignment         `json:"rows"`
	ByDepartment []DepartmentProgress `json:"by_department"`
	Filters      ReportFilters        `json:"filters"`
	ExportedAt   time.Time            `json:"exported_at"`
}

type ReportFilters struct {
	CampaignID *uuid.UUID `json:"campaign_id,omitempty"`
	CourseID   *uuid.UUID `json:"course_id,omitempty"`
	Department string     `json:"department,omitempty"`
	ManagerID  *uuid.UUID `json:"manager_id,omitempty"`
	Status     string     `json:"status,omitempty"`
	From       *time.Time `json:"from,omitempty"`
	To         *time.Time `json:"to,omitempty"`
	Search     string     `json:"search,omitempty"`
	OnlyMyTeam bool       `json:"only_my_team,omitempty"`
}

// ---------------------------------------------------------------------
// Supporting records
// ---------------------------------------------------------------------

type Reminder struct {
	ID           uuid.UUID  `json:"id"`
	AssignmentID *uuid.UUID `json:"assignment_id,omitempty"`
	LearnerID    *uuid.UUID `json:"learner_id,omitempty"`
	LearnerName  *string    `json:"learner_name,omitempty"`
	CampaignID   *uuid.UUID `json:"campaign_id,omitempty"`
	Channel      string     `json:"channel"`
	Subject      *string    `json:"subject,omitempty"`
	Body         *string    `json:"body,omitempty"`
	SentBy       *uuid.UUID `json:"sent_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type AuditEntry struct {
	ID        int64          `json:"id"`
	OrgID     *uuid.UUID     `json:"org_id,omitempty"`
	ActorID   *uuid.UUID     `json:"actor_id,omitempty"`
	Action    string         `json:"action"`
	Entity    string         `json:"entity"`
	EntityID  *string        `json:"entity_id,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	IP        *string        `json:"ip,omitempty"`
	UserAgent *string        `json:"user_agent,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type ImportResult struct {
	Total   int      `json:"total_rows"`
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors"`
}

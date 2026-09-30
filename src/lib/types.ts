/**
 * Types mirrored from the Go API (internal/domain/models.go).
 * Field names match the JSON tags exactly, so the wire format is the
 * contract. Anything the API can omit is optional here.
 */

export type Role = "super_admin" | "org_admin" | "manager" | "learner";
export type UserStatus = "active" | "invited" | "inactive";
export type CourseStatus = "draft" | "published" | "archived";
export type LessonKind = "content" | "assessment" | "mixed";
export type BlockType =
  | "text"
  | "video"
  | "image"
  | "pdf"
  | "pptx"
  | "file"
  | "embed"
  | "quiz";
export type QuestionType = "single_choice" | "multi_choice" | "short_text" | "long_text";
export type CampaignStatus = "draft" | "active" | "closed";
export type AudienceType = "all" | "department" | "users";
export type AssignmentStatus =
  | "not_started"
  | "in_progress"
  | "pending_review"
  | "passed"
  | "failed"
  | "overdue";
export type AttemptStatus = "in_progress" | "pending_review" | "passed" | "failed";

export interface Organisation {
  id: string;
  name: string;
  slug: string;
  industry?: string;
  country?: string;
  timezone: string;
  is_demo: boolean;
  created_at: string;
}

export interface User {
  id: string;
  auth_user_id?: string;
  org_id: string;
  email: string;
  full_name: string;
  role: Role;
  department?: string;
  employee_number?: string;
  job_title?: string;
  phone?: string;
  status: UserStatus;
  avatar_url?: string;
  manager_id?: string;
  manager_name?: string;
  must_reset_password: boolean;
  last_seen_at?: string;
  created_at: string;
  updated_at: string;
  assigned_count?: number;
  completed_count?: number;
  passed_count?: number;
  progress_percent?: number;
}

export interface TeamMember {
  status: AssignmentStatus;
  course_title?: string;
  progress_percent: number;
  final_score?: number;
  due_date?: string;
  last_activity_at?: string;
}

export interface Asset {
  id: string;
  org_id: string;
  uploaded_by?: string;
  kind: string;
  status: string;
  original_name: string;
  storage_path: string;
  bucket: string;
  mime_type?: string;
  size_bytes: number;
  checksum?: string;
  page_count: number;
  chapter_count: number;
  parse_error?: string;
  download_url?: string;
  created_at: string;
  updated_at: string;
}

/**
 * Page metadata for a pdf/pptx lesson block.
 *
 * Deliberately no text_content and no file_url: the player renders the
 * original document itself with pdf.js, so shipping extracted text is what
 * made a PDF block display as unformatted plain text.
 */
export interface RenderablePage {
  /** 1-based physical page in the served document. */
  page_number: number;
  /** True when the page is a blank chapter divider. */
  is_break: boolean;
  chapter_title?: string;
}

export interface Block {
  id: string;
  lesson_id: string;
  org_id: string;
  type: BlockType;
  position: number;
  title?: string;
  body?: string;
  asset_id?: string;
  page_from?: number;
  page_to?: number;
  chapter_from?: number;
  chapter_to?: number;
  created_at: string;
  updated_at: string;
  asset?: Asset;
  url?: string;
  pages?: RenderablePage[];
  chapter_count?: number;
  seen_pages?: number[];
}

export interface Course {
  id: string;
  org_id: string;
  title: string;
  code?: string;
  description?: string;
  category?: string;
  cover_asset_id?: string;
  cover_url?: string;
  status: CourseStatus;
  estimated_minutes: number;
  created_by?: string;
  published_at?: string;
  created_at: string;
  updated_at: string;
  module_count: number;
  lesson_count: number;
  block_count: number;
  question_count: number;
  total_points: number;
  duration_minutes: number;
  assigned_learners?: number;
  completed_learners?: number;
  completion_percent?: number;
}

export interface Lesson {
  id: string;
  module_id: string;
  course_id: string;
  org_id: string;
  title: string;
  summary?: string;
  kind: LessonKind;
  position: number;
  estimated_minutes: number;
  created_at: string;
  updated_at: string;
  blocks?: Block[];
  block_count: number;
  question_count: number;
  status?: string;
  completed_at?: string;
}

export interface Module {
  id: string;
  course_id: string;
  org_id: string;
  title: string;
  description?: string;
  position: number;
  created_at: string;
  updated_at: string;
  lessons?: Lesson[];
  lesson_count: number;
  duration_minutes: number;
}

export interface OutlineStats {
  modules: number;
  lessons: number;
  blocks: number;
  questions: number;
  total_points: number;
  duration_minutes: number;
  chapters: number;
}

export interface CourseOutline {
  course: Course;
  modules: Module[];
  stats: OutlineStats;
}

export interface Option {
  id: string;
  question_id: string;
  label: string;
  /** Stripped by the API for learners. */
  is_correct?: boolean;
  position: number;
}

export interface Question {
  id: string;
  course_id: string;
  lesson_id?: string;
  org_id: string;
  type: QuestionType;
  prompt: string;
  hint?: string;
  explanation?: string;
  points: number;
  position: number;
  min_length?: number;
  max_length?: number;
  is_required: boolean;
  options?: Option[];
  created_at: string;
  updated_at: string;
}

export interface QuestionPaper {
  attempt_id?: string;
  assignment_id: string;
  course_id: string;
  course_title: string;
  pass_mark: number;
  max_attempts: number;
  attempts_used: number;
  total_points: number;
  question_count: number;
  duration_minutes?: number;
  questions: Question[];
}

export interface Campaign {
  id: string;
  org_id: string;
  course_id: string;
  course_title?: string;
  name: string;
  description?: string;
  status: CampaignStatus;
  audience_type: AudienceType;
  audience_department?: string;
  due_date?: string;
  pass_mark: number;
  issue_certificate: boolean;
  max_attempts: number;
  require_learning: boolean;
  created_by?: string;
  launched_at?: string;
  closed_at?: string;
  created_at: string;
  updated_at: string;
  audience_count: number;
  assigned_count: number;
  started_count: number;
  completed_count: number;
  passed_count: number;
  failed_count: number;
  overdue_count: number;
  progress_percent: number;
  average_score?: number;
}

export interface Assignment {
  id: string;
  org_id: string;
  campaign_id: string;
  campaign_name?: string;
  course_id: string;
  course_title: string;
  learner_id: string;
  learner_name?: string;
  learner_email?: string;
  department?: string;
  manager_id?: string;
  manager_name?: string;
  status: AssignmentStatus;
  assigned_at: string;
  due_date?: string;
  started_at?: string;
  last_activity_at: string;
  completed_at?: string;
  lessons_total: number;
  lessons_done: number;
  progress_percent: number;
  attempts_used: number;
  best_score?: number;
  final_score?: number;
  passed_at?: string;
  is_overdue: boolean;
  certificates_issued: number;
  overdue_notified_at?: string;
}

export interface Certificate {
  id: string;
  org_id: string;
  learner_id: string;
  course_id: string;
  campaign_id?: string;
  attempt_id?: string;
  certificate_number: string;
  verification_code: string;
  learner_name: string;
  course_title: string;
  score: number;
  completed_at: string;
  issued_at: string;
  revoked_at?: string;
  revoked_reason?: string;
  org_name?: string;
  share_url?: string;
  render_url?: string;
  valid: boolean;
}

export interface Answer {
  id: string;
  attempt_id: string;
  question_id: string;
  response_text?: string;
  selected_option_ids: string[];
  is_correct?: boolean;
  points_awarded?: number;
  auto_graded: boolean;
  feedback?: string;
  graded_by?: string;
  graded_at?: string;
  question?: Question;
  options?: Option[];
}

export interface Attempt {
  id: string;
  assignment_id: string;
  learner_id: string;
  attempt_number: number;
  status: AttemptStatus;
  max_score: number;
  score: number;
  percent: number;
  passed?: boolean;
  started_at: string;
  submitted_at?: string;
  graded_at?: string;
  graded_by?: string;
  answers?: Answer[];
  pending_manual_grade_count: number;
  pass_mark: number;
  review_needed: boolean;
}

export interface LearnerDashboard {
  user: User;
  overall_progress_percent: number;
  assigned_count: number;
  in_progress_count: number;
  completed_count: number;
  passed_count: number;
  failed_count: number;
  overdue_count: number;
  required_training: Assignment[];
  certificates: Certificate[];
  greeting: string;
}

export interface DashboardKPIs {
  employees: number;
  active_employees: number;
  departments: number;
  courses: number;
  published_courses: number;
  draft_courses: number;
  active_campaigns: number;
  assigned: number;
  started: number;
  completed: number;
  passed: number;
  failed: number;
  overdue: number;
  pending_review: number;
  completion_percent: number;
  pass_rate_percent: number;
  certificates_issued: number;
  average_score?: number;
}

export interface AttentionItem {
  type: "overdue" | "failed" | "due_soon" | "pending_review";
  severity: string;
  learner_id: string;
  learner_name: string;
  course_title: string;
  detail: string;
  due_date?: string;
  url?: string;
}

export interface DepartmentProgress {
  department: string;
  headcount: number;
  assigned: number;
  completed: number;
  passed: number;
  overdue: number;
  completion_percent: number;
  pass_rate_percent: number;
}

export interface Dashboard {
  kpis: DashboardKPIs;
  active_campaigns: Campaign[];
  attention_required: AttentionItem[];
  department_progress: DepartmentProgress[];
  recent_certificates: Certificate[];
  generated_at: string;
}

export interface StatusBreakdown {
  not_started: number;
  in_progress: number;
  pending_review: number;
  passed: number;
  failed: number;
  overdue: number;
  total: number;
  pass_rate_percent: number;
  average_score_percent: number;
}

export interface ReportFilters {
  campaign_id?: string;
  course_id?: string;
  department?: string;
  manager_id?: string;
  status?: string;
  from?: string;
  to?: string;
  search?: string;
  only_my_team?: boolean;
}

export interface ReportSummary {
  breakdown: StatusBreakdown;
  rows: Assignment[];
  by_department: DepartmentProgress[];
  filters: ReportFilters;
  exported_at: string;
}

export interface ImportResult {
  total_rows: number;
  created: number;
  updated: number;
  skipped: number;
  failed: number;
  errors: string[];
}

export interface AuthResponse {
  access_token: string;
  refresh_token?: string;
  token_type: string;
  expires_at: string;
  expires_in: number;
  user: User;
  org?: Organisation;
  must_change_password: boolean;
}

/** Generic list envelope returned by the paginated endpoints. */
export interface Paginated<T> {
  items: T[];
  total?: number;
  page?: number;
  per_page?: number;
}

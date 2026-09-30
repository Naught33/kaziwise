/** Display helpers. Kept in one place so labels stay consistent. */

import type {
  AssignmentStatus,
  AttemptStatus,
  CampaignStatus,
  CourseStatus,
  QuestionType,
  Role,
  UserStatus,
} from "./types";

/** "12 Mar 2026" */
export function formatDate(iso?: string | null): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleDateString(undefined, { day: "2-digit", month: "short", year: "numeric" });
}

/** "12 Mar 2026, 14:30" */
export function formatDateTime(iso?: string | null): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  return `${formatDate(iso)}, ${d.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  })}`;
}

/** Human relative time, e.g. "3 days ago", used for last activity. */
export function relativeTime(iso?: string | null): string {
  if (!iso) return "Never";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "Never";

  const diffMs = Date.now() - d.getTime();
  const abs = Math.abs(diffMs);
  const future = diffMs < 0;

  const mins = Math.round(abs / 60_000);
  if (mins < 1) return "Just now";
  if (mins < 60) return future ? `in ${mins} min` : `${mins} min ago`;

  const hours = Math.round(mins / 60);
  if (hours < 24) return future ? `in ${hours}h` : `${hours}h ago`;

  const days = Math.round(hours / 24);
  if (days < 31) return future ? `in ${days}d` : `${days} days ago`;

  const months = Math.round(days / 30);
  if (months < 12) return future ? `in ${months}mo` : `${months} months ago`;

  const years = Math.round(months / 12);
  return future ? `in ${years}y` : `${years} years ago`;
}

/** Whole percent. The API already sends 0-100. */
export function pct(value?: number | null): number {
  if (value === undefined || value === null || Number.isNaN(value)) return 0;
  return Math.max(0, Math.min(100, Math.round(value)));
}

export function formatPct(value?: number | null): string {
  return `${pct(value)}%`;
}

/** "1 h 25 min" for estimated course durations. */
export function formatDuration(minutes?: number | null): string {
  if (!minutes || minutes <= 0) return "-";
  if (minutes < 60) return `${minutes} min`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m ? `${h} h ${m} min` : `${h} h`;
}

export function formatBytes(bytes?: number | null): string {
  if (!bytes) return "-";
  const units = ["B", "KB", "MB", "GB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/** Initials for the avatar chip. */
export function initials(name?: string | null): string {
  if (!name) return "?";
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2);
  return (parts[0][0] + parts[parts.length - 1][0]).slice(0, 2);
}

/** First name only, for greetings. */
export function firstName(name?: string | null): string {
  return (name ?? "").trim().split(/\s+/)[0] ?? "";
}

/* ------------------------------------------------------------------ */
/* Label maps. Status text always accompanies the colour, never        */
/* replaces it (design system rule: never rely on colour alone).      */
/* ------------------------------------------------------------------ */

export const ROLE_LABELS: Record<Role, string> = {
  super_admin: "Super Admin",
  org_admin: "Organisation Admin",
  manager: "Manager",
  learner: "Learner",
};

export const USER_STATUS_LABELS: Record<UserStatus, string> = {
  active: "Active",
  invited: "Invited",
  inactive: "Inactive",
};

export const COURSE_STATUS_LABELS: Record<CourseStatus, string> = {
  draft: "Draft",
  published: "Published",
  archived: "Archived",
};

export const CAMPAIGN_STATUS_LABELS: Record<CampaignStatus, string> = {
  draft: "Draft",
  active: "Active",
  closed: "Closed",
};

export const ASSIGNMENT_STATUS_LABELS: Record<AssignmentStatus, string> = {
  not_started: "Not Started",
  in_progress: "In Progress",
  pending_review: "Awaiting Review",
  passed: "Passed",
  failed: "Failed",
  overdue: "Overdue",
};

export const ATTEMPT_STATUS_LABELS: Record<AttemptStatus, string> = {
  in_progress: "In Progress",
  pending_review: "Awaiting Review",
  passed: "Passed",
  failed: "Failed",
};

export const QUESTION_TYPE_LABELS: Record<QuestionType, string> = {
  single_choice: "Multiple choice",
  multi_choice: "Check all that apply",
  short_text: "Short answer",
  long_text: "Long answer",
};

export const QUESTION_TYPE_HINTS: Record<QuestionType, string> = {
  single_choice: "Learner picks one option.",
  multi_choice: "Learner can select several options.",
  short_text: "One or two sentences. Set a length limit if you want one.",
  long_text: "Paragraphs. Set a maximum length.",
};

export const BLOCK_TYPE_LABELS: Record<string, string> = {
  text: "Text",
  video: "Video",
  image: "Image",
  pdf: "PDF",
  pptx: "Slides",
  file: "File",
  embed: "Embed",
  quiz: "Knowledge check",
};

export const AUDIENCE_LABELS: Record<string, string> = {
  all: "Entire company",
  department: "Department",
  users: "Selected employees",
};

/** Does this role get to manage people, courses and campaigns? */
export function isStaff(role?: Role | null): boolean {
  return role === "super_admin" || role === "org_admin" || role === "manager";
}

/** Only these roles may write. Managers are read-only outside their team. */
export function canManage(role?: Role | null): boolean {
  return role === "super_admin" || role === "org_admin";
}

/** Managers and admins can see reporting. */
export function canViewReports(role?: Role | null): boolean {
  return isStaff(role);
}

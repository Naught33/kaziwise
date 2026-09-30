import { useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useApi, useDebounced } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import {
  canManage,
  COURSE_STATUS_LABELS,
  formatDuration,
  formatPct,
  relativeTime,
} from "../lib/format";
import type { Course, CourseStatus } from "../lib/types";
import { PageHeader } from "../components/Layout";
import {
  Alert,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Icon,
  Modal,
  ProgressBar,
  Skeleton,
  type Tone,
} from "../components/ui";
import { Fieldset, SelectField, TextAreaField, TextField } from "../components/Field";

/** 03 - Course Library. Drafts must be clearly distinguishable. */
export default function CourseLibrary() {
  const { user } = useAuth();
  const nav = useNavigate();
  const mayEdit = canManage(user?.role);

  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const debounced = useDebounced(search);

  const query = useMemo(
    () => ({ search: debounced || undefined, status: status || undefined, per_page: 100 }),
    [debounced, status],
  );

  const { data, error, loading, initial, reload } = useApi<Course[]>(
    (signal) => api.page<Course>("/v1/courses", query, signal).then((p) => p.items),
    [query],
  );

  const drafts = (data ?? []).filter((c) => c.status === "draft");
  const published = (data ?? []).filter((c) => c.status !== "draft");

  return (
    <>
      <PageHeader
        title="Course Library"
        subtitle="Reusable training content. Publish a course before assigning it in a campaign."
        actions={
          mayEdit && (
            <Button icon={<Icon name="plus" size={16} />} onClick={() => setCreateOpen(true)}>
              Create Course
            </Button>
          )
        }
      />

      <div className="card" style={{ marginBottom: "var(--s4)" }}>
        <div className="card-body">
          <div className="filter-bar">
            <div className="search-box">
              <Icon name="search" size={16} />
              <input
                className="input"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search courses"
                aria-label="Search courses"
              />
            </div>
            <select
              className="select"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              aria-label="Filter by status"
              style={{ maxWidth: 180 }}
            >
              <option value="">All statuses</option>
              <option value="draft">Draft</option>
              <option value="published">Published</option>
              <option value="archived">Archived</option>
            </select>
          </div>
        </div>
      </div>

      {error && !initial ? (
        <ErrorState error={error} onRetry={reload} />
      ) : loading && initial ? (
        <div className="grid grid-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div className="card" key={i}>
              <div className="card-body">
                <Skeleton height={18} width="70%" />
                <Skeleton height={12} width="45%" />
                <div style={{ height: 12 }} />
                <Skeleton height={8} />
              </div>
            </div>
          ))}
        </div>
      ) : data && data.length ? (
        <div className="stack">
          {drafts.length > 0 && (
            <section>
              <div className="section-label">
                <Badge tone="warning">Drafts</Badge>
                <span className="small muted">
                  {drafts.length} not yet available for assignment
                </span>
              </div>
              <div className="grid grid-3">
                {drafts.map((c) => (
                  <CourseCard key={c.id} course={c} onOpen={() => nav(`/courses/${c.id}/builder`)} />
                ))}
              </div>
            </section>
          )}
          {published.length > 0 && (
            <section>
              <div className="section-label">
                <Badge tone="success">Live</Badge>
                <span className="small muted">
                  {published.length} ready to assign
                </span>
              </div>
              <div className="grid grid-3">
                {published.map((c) => (
                  <CourseCard key={c.id} course={c} onOpen={() => nav(`/courses/${c.id}/builder`)} />
                ))}
              </div>
            </section>
          )}
        </div>
      ) : (
        <div className="card">
          <EmptyState
            icon="book"
            title={search || status ? "No courses match" : "No courses yet"}
            text={
              search || status
                ? "Try a different search, or clear the status filter."
                : "A course holds modules, lessons and content. Create one, add your material, then publish it so it can be assigned."
            }
            action={
              mayEdit && !search && !status ? (
                <Button size="sm" onClick={() => setCreateOpen(true)}>
                  Create Course
                </Button>
              ) : undefined
            }
          />
        </div>
      )}

      {createOpen && (
        <CreateCourseModal
          onClose={() => setCreateOpen(false)}
          onCreated={(c) => nav(`/courses/${c.id}/builder`)}
        />
      )}
    </>
  );
}

function courseTone(s: CourseStatus): Tone {
  if (s === "published") return "success";
  if (s === "draft") return "warning";
  return "neutral";
}

function CourseCard({ course, onOpen }: { course: Course; onOpen: () => void }) {
  return (
    <article
      className={`card course-card${course.status === "draft" ? " course-card-draft" : ""}`}
    >
      <div className="course-card-top">
        {course.cover_url ? (
          <img className="course-cover" src={course.cover_url} alt="" />
        ) : (
          <div className="course-cover course-cover-placeholder" aria-hidden="true">
            <Icon name="book" size={26} />
          </div>
        )}
        <div className="course-card-status">
          <Badge tone={courseTone(course.status)}>{COURSE_STATUS_LABELS[course.status]}</Badge>
        </div>
      </div>

      <div className="course-card-body">
        <h3>{course.title}</h3>
        {course.description && (
          <p className="small soft clamp-2">{course.description}</p>
        )}

        <div className="course-meta">
          <span>
            <Icon name="layers" size={14} /> {course.module_count} modules
          </span>
          <span>
            <Icon name="book" size={14} /> {course.lesson_count} lessons
          </span>
          <span>
            <Icon name="clock" size={14} /> {formatDuration(course.duration_minutes)}
          </span>
        </div>

        {course.question_count > 0 && (
          <div className="course-meta">
            <span>
              <Icon name="check" size={14} /> {course.question_count} questions
            </span>
          </div>
        )}

        {course.assigned_learners ? (
          <div style={{ marginTop: "auto", paddingTop: "var(--s3)" }}>
            <ProgressBar value={course.completion_percent} />
            <div className="cell-sub">
              {course.completed_learners ?? 0} of {course.assigned_learners} learners completed
            </div>
          </div>
        ) : (
          <div className="cell-sub" style={{ marginTop: "auto" }}>
            {course.status === "published"
              ? `Published ${relativeTime(course.published_at)}`
              : `Edited ${relativeTime(course.updated_at)}`}
          </div>
        )}
      </div>

      <div className="course-card-foot">
        <Button size="sm" variant="secondary" onClick={onOpen}>
          Open Course
        </Button>
      </div>
    </article>
  );
}

function CreateCourseModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (course: Course) => void;
}) {
  const [form, setForm] = useState({
    title: "",
    description: "",
    category: "",
    code: "",
    estimated_minutes: "",
  });
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (k: keyof typeof form) => (v: string) => setForm((f) => ({ ...f, [k]: v }));

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setFields({});
    setBusy(true);
    try {
      const course = await api.post<Course>("/v1/courses", {
        title: form.title.trim(),
        ...(form.description.trim() ? { description: form.description.trim() } : {}),
        ...(form.category.trim() ? { category: form.category.trim() } : {}),
        ...(form.code.trim() ? { code: form.code.trim() } : {}),
        ...(form.estimated_minutes
          ? { estimated_minutes: Number(form.estimated_minutes) }
          : {}),
      });
      onCreated(course);
    } catch (err) {
      if (err instanceof ApiError) {
        setFields(err.fieldMessages);
        setError(err.message);
      } else {
        setError("Cannot reach the KaziWise server.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="Create course"
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={busy}>
            Create and build
          </Button>
        </>
      }
    >
      {error && <Alert tone="error">{error}</Alert>}
      <form onSubmit={submit} noValidate>
        <Fieldset
          title="Course details"
          description="You can add modules, lessons and assessments after this."
        >
          <TextField
            label="Course title"
            value={form.title}
            onChange={set("title")}
            error={fields.title}
            placeholder="Workplace Safety Essentials"
            required
            autoFocus
          />
          <TextAreaField
            label="Description"
            value={form.description}
            onChange={set("description")}
            error={fields.description}
            placeholder="What will employees learn from this course?"
          />
          <div className="field-row">
            <TextField
              label="Category"
              value={form.category}
              onChange={set("category")}
              error={fields.category}
              placeholder="Compliance"
            />
            <TextField
              label="Course code"
              value={form.code}
              onChange={set("code")}
              error={fields.code}
              placeholder="SAFE-101"
            />
          </div>
          <TextField
            label="Estimated minutes"
            type="number"
            value={form.estimated_minutes}
            onChange={set("estimated_minutes")}
            error={fields.estimated_minutes}
            hint="Optional. Used as a fallback when lessons have no duration."
          />
        </Fieldset>
      </form>
    </Modal>
  );
}

export { formatPct, SelectField };

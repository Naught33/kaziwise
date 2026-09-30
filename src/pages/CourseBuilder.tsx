import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useApi } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import {
  BLOCK_TYPE_LABELS,
  canManage,
  COURSE_STATUS_LABELS,
  formatDuration,
  formatPct,
  QUESTION_TYPE_HINTS,
  QUESTION_TYPE_LABELS,
} from "../lib/format";
import type {
  Block,
  BlockType,
  CourseOutline,
  Lesson,
  Module,
  Question,
  QuestionType,
} from "../lib/types";
import {
  Alert,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Icon,
  Modal,
  Skeleton,
  Spinner,
  type Tone,
} from "../components/ui";
import {
  CheckboxField,
  Fieldset,
  SelectField,
  TextAreaField,
  TextField,
} from "../components/Field";

type Tab = "lesson" | "assessment" | "resources";

/**
 * 04 - Course Builder.
 * Left: modules and lessons. Centre: lesson editor. Right: settings.
 * Tabs: Lesson, Assessment, Resources.
 */
export default function CourseBuilder() {
  const { courseId = "" } = useParams();
  const nav = useNavigate();
  const { user } = useAuth();
  const mayEdit = canManage(user?.role);

  const outline = useApi<CourseOutline>(
    (signal) => api.get<CourseOutline>(`/v1/courses/${courseId}/outline`, { signal }),
    [courseId],
  );

  const [selectedLessonId, setSelectedLessonId] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("lesson");
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const course = outline.data?.course;
  const modules = outline.data?.modules ?? [];

  // Default the editor to the first lesson once the outline lands.
  useEffect(() => {
    if (selectedLessonId) return;
    const first = modules[0]?.lessons?.[0];
    if (first) setSelectedLessonId(first.id);
  }, [modules, selectedLessonId]);

  const selected = useMemo(() => {
    for (const m of modules) {
      const l = m.lessons?.find((x) => x.id === selectedLessonId);
      if (l) return l;
    }
    return null;
  }, [modules, selectedLessonId]);

  const questions = useApi<Question[]>(
    (signal) => api.get<Question[]>(`/v1/courses/lessons/${selectedLessonId}/questions`, { signal }),
    [selectedLessonId],
  );

  async function run(key: string, fn: () => Promise<unknown>, okMessage?: string) {
    setBusyAction(key);
    setActionError(null);
    setNotice(null);
    try {
      await fn();
      if (okMessage) setNotice(okMessage);
      outline.reload();
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "That action could not be completed.");
    } finally {
      setBusyAction(null);
    }
  }

  if (outline.initial) {
    return (
      <div className="stack">
        <Skeleton height={40} width="40%" />
        <div className="builder">
          <div className="card">
            <div className="card-body stack-sm">
              {Array.from({ length: 6 }).map((_, i) => (
                <Skeleton key={i} height={38} />
              ))}
            </div>
          </div>
          <div className="card">
            <div className="card-body">
              <Skeleton height={220} />
            </div>
          </div>
        </div>
      </div>
    );
  }

  if (outline.error) return <ErrorState error={outline.error} onRetry={outline.reload} />;

  if (!course) {
    return (
      <div className="card">
        <EmptyState
          icon="book"
          title="Course not found"
          text="It may have been deleted, or the link may be wrong."
          action={<Button onClick={() => nav("/courses")}>Back to library</Button>}
        />
      </div>
    );
  }

  const published = course.status === "published";

  return (
    <div className="stack">
      {/* Builder header: breadcrumb back, status, preview and publish */}
      <div className="builder-head">
        <div style={{ minWidth: 0 }}>
          <button className="link-back" onClick={() => nav("/courses")} type="button">
            <Icon name="chevronLeft" size={14} /> Course Library
          </button>
          <div className="row" style={{ gap: 10, marginTop: 4 }}>
            <h1 style={{ fontSize: 22 }}>{course.title}</h1>
            <Badge tone={published ? "success" : "warning"}>
              {COURSE_STATUS_LABELS[course.status]}
            </Badge>
          </div>
          <div className="small muted">
            {outline.data?.stats.modules} modules &middot; {outline.data?.stats.lessons} lessons
            &middot; {outline.data?.stats.questions} questions &middot;{" "}
            {formatDuration(outline.data?.stats.duration_minutes)}
          </div>
        </div>

        <div className="row-wrap">
          <Button
            variant="secondary"
            icon={<Icon name="play" size={16} />}
            onClick={() => window.open(`/learn/courses/${course.id}`, "_blank")}
          >
            Preview
          </Button>
          {mayEdit && (
            <>
              <Button
                variant="secondary"
                onClick={() =>
                  run("save", async () => {
                    // Save preserves the draft status; nothing to send unless
                    // the title changed, so just re-read to confirm.
                    await api.get(`/v1/courses/${course.id}`);
                  }, "Draft saved.")
                }
                loading={busyAction === "save"}
              >
                Save Draft
              </Button>
              {published ? (
                <Button
                  variant="secondary"
                  onClick={() =>
                    run("unpublish", () => api.post(`/v1/courses/${course.id}/unpublish`))
                  }
                  loading={busyAction === "unpublish"}
                >
                  Unpublish
                </Button>
              ) : (
                <Button
                  onClick={() => run("publish", () => api.post(`/v1/courses/${course.id}/publish`), "Course published.")}
                  loading={busyAction === "publish"}
                  disabled={!mayEdit}
                >
                  Publish Course
                </Button>
              )}
            </>
          )}
        </div>
      </div>

      {actionError && (
        <Alert tone="error" title="Action failed">
          {actionError}
        </Alert>
      )}
      {notice && <Alert tone="success">{notice}</Alert>}
      {!mayEdit && (
        <Alert tone="info">
          You have read-only access to this course. An organisation administrator can make
          changes.
        </Alert>
      )}

      <div className="builder">
        {/* Left panel: modules and lessons */}
        <aside className="card builder-left">
          <div className="card-header">
            <div className="card-title">Outline</div>
            {mayEdit && (
              <Button
                size="sm"
                variant="ghost"
                icon={<Icon name="plus" size={14} />}
                onClick={() => {
                  const name = window.prompt("Module title");
                  if (name?.trim()) {
                    void run("module", () =>
                      api.post(`/v1/courses/${course.id}/modules`, { title: name.trim() }),
                    );
                  }
                }}
              >
                Module
              </Button>
            )}
          </div>
          <div className="card-body card-body-flush builder-tree">
            {modules.length === 0 ? (
              <EmptyState
                icon="layers"
                title="No modules yet"
                text="A module groups related lessons, for example one chapter of a policy."
              />
            ) : (
              modules.map((m) => (
                <ModuleNode
                  key={m.id}
                  module={m}
                  courseId={course.id}
                  selectedLessonId={selectedLessonId}
                  onSelectLesson={setSelectedLessonId}
                  mayEdit={mayEdit}
                  onChanged={outline.reload}
                  onError={setActionError}
                />
              ))
            )}
          </div>
        </aside>

        {/* Centre + right: the editor and its settings */}
        <div className="builder-main">
          {selected ? (
            <>
              <nav className="tabs" role="tablist">
                {(
                  [
                    ["lesson", "Lesson", "book"],
                    ["assessment", "Assessment", "check"],
                    ["resources", "Resources", "download"],
                  ] as const
                ).map(([key, label, icon]) => (
                  <button
                    key={key}
                    role="tab"
                    type="button"
                    aria-selected={tab === key}
                    className={`tab${tab === key ? " active" : ""}`}
                    onClick={() => setTab(key)}
                  >
                    <Icon name={icon} size={15} /> {label}
                  </button>
                ))}
              </nav>

              {tab === "lesson" && (
                <LessonEditor
                  lessonId={selected.id}
                  lesson={selected}
                  loading={false}
                  mayEdit={mayEdit}
                  onChanged={() => {
                    questions.reload();
                    outline.reload();
                  }}
                  onError={setActionError}
                />
              )}

              {tab === "assessment" && (
                <AssessmentEditor
                  lessonId={selected.id}
                  questions={questions.data ?? []}
                  loading={questions.loading && questions.initial}
                  mayEdit={mayEdit}
                  onChanged={questions.reload}
                  onError={setActionError}
                />
              )}

              {tab === "resources" && (
                <ResourcesPanel lesson={selected} mayEdit={mayEdit} />
              )}
            </>
          ) : (
            <div className="card">
              <EmptyState
                icon="edit"
                title="Select a lesson"
                text="Pick a lesson from the outline on the left, or create one to start adding content."
              />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Outline tree                                                        */
/* ------------------------------------------------------------------ */

function ModuleNode({
  module,
  courseId,
  selectedLessonId,
  onSelectLesson,
  mayEdit,
  onChanged,
  onError,
}: {
  module: Module;
  courseId: string;
  selectedLessonId: string | null;
  onSelectLesson: (id: string) => void;
  mayEdit: boolean;
  onChanged: () => void;
  onError: (m: string) => void;
}) {
  const [open, setOpen] = useState(true);
  const [busy, setBusy] = useState(false);
  const lessons = module.lessons ?? [];

  async function addLesson() {
    const title = window.prompt("Lesson title");
    if (!title?.trim()) return;
    setBusy(true);
    try {
      await api.post(`/v1/courses/${courseId}/modules/${module.id}/lessons`, {
        title: title.trim(),
      });
      onChanged();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "Could not add the lesson.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="tree-module">
      <div className="tree-module-head">
        <button
          type="button"
          className="tree-toggle"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label={open ? `Collapse ${module.title}` : `Expand ${module.title}`}
        >
          <Icon name={open ? "chevronDown" : "chevronRight"} size={14} />
        </button>
        <span className="tree-module-title">{module.title}</span>
        <span className="tree-count">{lessons.length}</span>
        {mayEdit && (
          <button
            className="icon-btn tree-add"
            onClick={addLesson}
            disabled={busy}
            aria-label={`Add lesson to ${module.title}`}
            type="button"
          >
            {busy ? <Spinner size={14} /> : <Icon name="plus" size={14} />}
          </button>
        )}
      </div>

      {open && (
        <ul className="tree-lessons">
          {lessons.length === 0 && (
            <li className="tree-empty">No lessons in this module yet.</li>
          )}
          {lessons.map((l) => (
            <li key={l.id}>
              <button
                type="button"
                className={`tree-lesson${l.id === selectedLessonId ? " active" : ""}`}
                onClick={() => onSelectLesson(l.id)}
              >
                <Icon name={l.kind === "assessment" ? "check" : "book"} size={14} />
                <span className="tree-lesson-title">{l.title}</span>
                {l.block_count > 0 && <span className="tree-count">{l.block_count}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Lesson editor: content blocks                                       */
/* ------------------------------------------------------------------ */

function LessonEditor({
  lessonId,
  lesson,
  loading,
  mayEdit,
  onChanged,
  onError,
}: {
  lessonId: string;
  lesson: Lesson;
  loading: boolean;
  mayEdit: boolean;
  onChanged: () => void;
  onError: (m: string) => void;
}) {
  const blocks = lesson.blocks ?? [];
  const [adding, setAdding] = useState<BlockType | null>(null);
  const [saving, setSaving] = useState(false);
  const [meta, setMeta] = useState({ title: "", summary: "", estimated_minutes: "" });

  useEffect(() => {
    setMeta({
      title: lesson.title ?? "",
      summary: lesson.summary ?? "",
      estimated_minutes: lesson.estimated_minutes ? String(lesson.estimated_minutes) : "",
    });
  }, [lesson.id, lesson.title, lesson.summary, lesson.estimated_minutes]);

  async function saveMeta() {
    setSaving(true);
    try {
      const minutes = Number(meta.estimated_minutes);
      if (!meta.title.trim()) {
        onError("A lesson needs a title.");
        setSaving(false);
        return;
      }
      if (meta.estimated_minutes && (!Number.isFinite(minutes) || minutes < 0)) {
        onError("Enter a valid number of minutes.");
        setSaving(false);
        return;
      }
      await api.patch(`/v1/courses/lessons/${lessonId}`, {
        title: meta.title.trim(),
        // Send summary and minutes even when cleared. trimOrNil turns an
        // empty string into a nil pointer server-side, so omitting the key
        // silently keeps the old value and the edit appears not to save.
        summary: meta.summary.trim(),
        estimated_minutes: Number.isFinite(minutes) && minutes > 0 ? minutes : 0,
      });
      onChanged();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "Could not save the lesson.");
    } finally {
      setSaving(false);
    }
  }

  async function removeBlock(block: Block) {
    if (!window.confirm(`Remove "${block.title || BLOCK_TYPE_LABELS[block.type]}"?`)) return;
    try {
      await api.del(`/v1/courses/blocks/${block.id}`);
      onChanged();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "Could not remove the block.");
    }
  }

  if (loading) {
    return (
      <div className="card">
        <div className="card-body">
          <Skeleton height={200} />
        </div>
      </div>
    );
  }

  return (
    <div className="stack">
      <div className="card">
        <div className="card-header">
          <div className="card-title">Lesson details</div>
          {mayEdit && (
            <Button size="sm" onClick={saveMeta} loading={saving} disabled={saving}>
              Save
            </Button>
          )}
        </div>
        <div className="card-body">
          <TextField
            label="Lesson title"
            value={meta.title}
            onChange={(v) => setMeta((m) => ({ ...m, title: v }))}
            disabled={!mayEdit}
            required
          />
          <TextAreaField
            label="Summary"
            value={meta.summary}
            onChange={(v) => setMeta((m) => ({ ...m, summary: v }))}
            disabled={!mayEdit}
            placeholder="One line on what this lesson covers."
          />
          <TextField
            label="Estimated minutes"
            type="number"
            value={meta.estimated_minutes}
            onChange={(v) => setMeta((m) => ({ ...m, estimated_minutes: v }))}
            disabled={!mayEdit}
          />
        </div>
      </div>

      {/* Content blocks supported in the MVP: Text, Video, Image and PDF */}
      <div className="card">
        <div className="card-header">
          <div>
            <div className="card-title">Content</div>
            <div className="card-sub">
              A blank page or slide in a PDF or deck starts a new chapter.
            </div>
          </div>
        </div>

        <div className="card-body card-body-flush">
          {blocks.length === 0 ? (
            <EmptyState
              icon="edit"
              title="This lesson is empty"
              text="Add a text block to explain the topic, upload slides or a PDF, or embed a video."
              action={
                mayEdit ? (
                  <BlockTypePicker onPick={(t) => setAdding(t)} />
                ) : undefined
              }
            />
          ) : (
            <ul className="block-list">
              {blocks.map((b) => (
                <li className="block-row" key={b.id}>
                  <div className="block-icon">
                    <Icon
                      name={
                        b.type === "video"
                          ? "play"
                          : b.type === "pdf" || b.type === "pptx"
                            ? "book"
                            : b.type === "image"
                              ? "layers"
                              : "edit"
                      }
                      size={16}
                    />
                  </div>
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div className="cell-strong">{b.title || BLOCK_TYPE_LABELS[b.type]}</div>
                    <div className="cell-sub">
                      {BLOCK_TYPE_LABELS[b.type]}
                      {b.chapter_count ? ` - ${b.chapter_count} chapters` : ""}
                      {b.page_from ? ` - pages ${b.page_from} to ${b.page_to ?? b.page_from}` : ""}
                    </div>
                    {b.body && <div className="small soft clamp-2">{b.body}</div>}
                  </div>
                  {mayEdit && (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => removeBlock(b)}
                      aria-label={`Remove ${b.title || BLOCK_TYPE_LABELS[b.type]}`}
                      icon={<Icon name="trash" size={15} />}
                    />
                  )}
                </li>
              ))}
            </ul>
          )}

          {mayEdit && blocks.length > 0 && (
            <div className="card-footer">
              <BlockTypePicker onPick={(t) => setAdding(t)} />
            </div>
          )}
        </div>
      </div>

      {adding && (
        <AddBlockModal
          lessonId={lessonId}
          type={adding}
          onClose={() => setAdding(null)}
          onSaved={() => {
            setAdding(null);
            onChanged();
          }}
          onError={onError}
        />
      )}
    </div>
  );
}

const BLOCK_CHOICES: { type: BlockType; label: string; hint: string }[] = [
  { type: "text", label: "Text", hint: "Rich text the learner reads" },
  { type: "video", label: "Video", hint: "A video URL or upload" },
  { type: "image", label: "Image", hint: "A diagram or photo" },
  { type: "pdf", label: "PDF", hint: "Pages, blank page = new chapter" },
  { type: "pptx", label: "Slides", hint: "PPTX, blank slide = new chapter" },
  { type: "file", label: "File", hint: "Any other download" },
  { type: "embed", label: "Embed", hint: "An external page" },
];

function BlockTypePicker({ onPick }: { onPick: (t: BlockType) => void }) {
  return (
    <div className="row-wrap">
      <span className="small muted" style={{ marginRight: 4 }}>
        Add content:
      </span>
      {BLOCK_CHOICES.map((c) => (
        <Button
          key={c.type}
          size="sm"
          variant="secondary"
          onClick={() => onPick(c.type)}
          title={c.hint}
        >
          {c.label}
        </Button>
      ))}
    </div>
  );
}

function AddBlockModal({
  lessonId,
  type,
  onClose,
  onSaved,
  onError,
}: {
  lessonId: string;
  type: BlockType;
  onClose: () => void;
  onSaved: () => void;
  onError: (m: string) => void;
}) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [chapters, setChapters] = useState<number | null>(null);
  const [assetId, setAssetId] = useState<string | null>(null);
  const [pageCount, setPageCount] = useState<number | null>(null);
  const [pageRange, setPageRange] = useState({ from: 1, to: 1 });
  const inputRef = useRef<HTMLInputElement>(null);

  const needsFile = type === "pdf" || type === "pptx" || type === "image" || type === "file";

  const paginated = type === "pdf" || type === "pptx";

  async function doUpload(): Promise<{ id: string; page_count: number } | null> {
    if (!file) return null;
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const asset = await api.upload<{ id: string; page_count: number; chapter_count: number }>(
        "/v1/assets/upload",
        fd,
      );
      setAssetId(asset.id);
      setChapters(asset.chapter_count || null);
      setPageCount(asset.page_count || 1);
      // A paginated block must carry a page range, so default the range to
      // the whole document the moment its length is known. Without this the
      // create request is rejected for a missing page_from/page_to.
      if (paginated) {
        const last = Math.max(1, asset.page_count || 1);
        setPageRange({ from: 1, to: last });
      }
      return { id: asset.id, page_count: asset.page_count || 1 };
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "The file could not be uploaded.");
      setBusy(false);
      return null;
    }
  }

  async function submit() {
    setBusy(true);
    try {
      let id = assetId;
      let uploaded: { id: string; page_count: number } | null = null;
      if (!id && file) uploaded = await doUpload();
      if (uploaded) id = uploaded.id;

      const payload: Record<string, unknown> = {
        type,
        ...(title.trim() ? { title: title.trim() } : {}),
        ...(body.trim() ? { body: body.trim() } : {}),
        ...(id ? { asset_id: id } : {}),
      };
      // PDFs and decks render page by page, so the range is mandatory.
      if (paginated) {
        const last = uploaded ? Math.max(1, uploaded.page_count) : Math.max(1, pageRange.to);
        payload.page_from = Math.max(1, pageRange.from || 1);
        payload.page_to = Math.min(last, Math.max(1, pageRange.to || last));
      }
      await api.post(`/v1/courses/lessons/${lessonId}/blocks`, payload);
      onSaved();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "The block could not be added.");
      setBusy(false);
    }
  }

  return (
    <Modal
      title={`Add ${BLOCK_TYPE_LABELS[type].toLowerCase()} block`}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={busy}>
            Add block
          </Button>
        </>
      }
    >
      <Fieldset title="Block settings">
        <TextField
          label="Title"
          value={title}
          onChange={setTitle}
          placeholder={BLOCK_TYPE_LABELS[type]}
          autoFocus
        />
        {needsFile && (
          <div className="field">
            <span className="field-label">File</span>
            <input
              ref={inputRef}
              className="input"
              type="file"
              accept={
                type === "pdf"
                  ? "application/pdf"
                  : type === "pptx"
                    ? ".pptx,.ppt"
                    : "image/*"
              }
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
            <span className="field-hint">
              {type === "pdf" || type === "pptx"
                ? "Leave a page or slide blank to start a new chapter. Blank pages are skipped when learners view it."
                : "Uploaded to KaziWise storage."}
            </span>
          </div>
        )}
        <TextAreaField
          label={type === "video" ? "Video URL" : "Body"}
          value={body}
          onChange={setBody}
          rows={type === "text" ? 8 : 3}
          placeholder={
            type === "video"
              ? "https://www.youtube.com/watch?v=..."
              : type === "text"
                ? "Explain the key points. Plain text and line breaks are fine."
                : "Optional notes or description."
          }
        />
        {paginated && file && (
          <div className="grid grid-2" style={{ gap: "var(--s3)" }}>
            <TextField
              label={type === "pptx" ? "First slide" : "First page"}
              value={String(pageRange.from)}
              onChange={(v) => setPageRange((r) => ({ ...r, from: Number(v) || 1 }))}
            />
            <TextField
              label={type === "pptx" ? "Last slide" : "Last page"}
              value={String(pageRange.to)}
              onChange={(v) => setPageRange((r) => ({ ...r, to: Number(v) || 1 }))}
            />
          </div>
        )}
      </Fieldset>

      {paginated && pageCount !== null && (
        <Alert tone="info" title={`${pageCount} ${type === "pptx" ? "slides" : "pages"}`}>
          This block shows {pageRange.from} to {pageRange.to}. Narrow the range to split the
          document across several lessons.
        </Alert>
      )}

      {chapters !== null && chapters > 0 && (
        <Alert tone="success" title={`${chapters} chapters detected`}>
          Chapter headings come from the text on the first page after each blank.
        </Alert>
      )}
    </Modal>
  );
}

/* ------------------------------------------------------------------ */
/* Assessment editor                                                   */
/* ------------------------------------------------------------------ */

function AssessmentEditor({
  lessonId,
  questions,
  loading,
  mayEdit,
  onChanged,
  onError,
}: {
  lessonId: string;
  questions: Question[];
  loading: boolean;
  mayEdit: boolean;
  onChanged: () => void;
  onError: (m: string) => void;
}) {
  const [open, setOpen] = useState(false);

  if (loading) {
    return (
      <div className="card">
        <div className="card-body">
          <Skeleton height={160} />
        </div>
      </div>
    );
  }

  const byType = (t: QuestionType) => questions.filter((q) => q.type === t).length;

  return (
    <div className="stack">
      <div className="card">
        <div className="card-header">
          <div>
            <div className="card-title">Assessment</div>
            <div className="card-sub">
              {questions.length} question{questions.length === 1 ? "" : "s"} &middot;{" "}
              {questions.reduce((s, q) => s + q.points, 0)} points
            </div>
          </div>
          {mayEdit && (
            <Button size="sm" icon={<Icon name="plus" size={14} />} onClick={() => setOpen(true)}>
              Add question
            </Button>
          )}
        </div>

        <div className="card-body">
          <div className="grid grid-2" style={{ gap: "var(--s3)" }}>
            {(Object.keys(QUESTION_TYPE_LABELS) as QuestionType[]).map((t) => (
              <div className="qtype-chip" key={t}>
                <div>
                  <div className="cell-strong">{QUESTION_TYPE_LABELS[t]}</div>
                  <div className="cell-sub">{QUESTION_TYPE_HINTS[t]}</div>
                </div>
                <Badge tone={byType(t) ? "primary" : "neutral"}>{byType(t)}</Badge>
              </div>
            ))}
          </div>
        </div>

        {questions.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>Question</th>
                  <th>Type</th>
                  <th>Points</th>
                  {mayEdit && <th />}
                </tr>
              </thead>
              <tbody>
                {questions.map((q, i) => (
                  <tr key={q.id}>
                    <td className="num muted">{i + 1}</td>
                    <td>
                      <div className="cell-strong clamp-2">{q.prompt}</div>
                      {q.options && q.options.length > 0 && (
                        <div className="cell-sub">
                          {q.options.map((o) => o.label).join(" | ")}
                        </div>
                      )}
                    </td>
                    <td>
                      <Badge tone="info" plain>
                        {QUESTION_TYPE_LABELS[q.type]}
                      </Badge>
                    </td>
                    <td className="num">{q.points}</td>
                    {mayEdit && (
                      <td>
                        <div className="td-actions">
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label="Delete question"
                            icon={<Icon name="trash" size={15} />}
                            onClick={async () => {
                              if (!window.confirm("Delete this question?")) return;
                              try {
                                await api.del(`/v1/courses/questions/${q.id}`);
                                onChanged();
                              } catch (err) {
                                onError(
                                  err instanceof ApiError ? err.message : "Could not delete it.",
                                );
                              }
                            }}
                          />
                        </div>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {open && (
        <AddQuestionModal
          lessonId={lessonId}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false);
            onChanged();
          }}
          onError={onError}
        />
      )}
    </div>
  );
}

function AddQuestionModal({
  lessonId,
  onClose,
  onSaved,
  onError,
}: {
  lessonId: string;
  onClose: () => void;
  onSaved: () => void;
  onError: (m: string) => void;
}) {
  const [type, setType] = useState<QuestionType>("single_choice");
  const [prompt, setPrompt] = useState("");
  const [points, setPoints] = useState("1");
  const [required, setRequired] = useState(true);
  const [minLength, setMinLength] = useState("");
  const [maxLength, setMaxLength] = useState("");
  const [options, setOptions] = useState<string[]>(["", ""]);
  const [explanation, setExplanation] = useState("");
  const [correct, setCorrect] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);

  const hasOptions = type === "single_choice" || type === "multi_choice";
  const hasLengths = type === "short_text" || type === "long_text";

  async function submit() {
    setBusy(true);
    try {
      await api.post(`/v1/courses/lessons/${lessonId}/questions`, {
        type,
        prompt: prompt.trim(),
        points: Number(points) || 1,
        is_required: required,
        ...(explanation.trim() ? { explanation: explanation.trim() } : {}),
        ...(hasLengths && minLength ? { min_length: Number(minLength) } : {}),
        ...(hasLengths && maxLength ? { max_length: Number(maxLength) } : {}),
        ...(hasOptions
          ? {
              options: options
                .map((label, i) => ({ label: label.trim(), is_correct: correct.includes(i) }))
                .filter((o) => o.label),
            }
          : {}),
      });
      onSaved();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "The question could not be saved.");
      setBusy(false);
    }
  }

  return (
    <Modal
      title="Add question"
      wide
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={busy}>
            Add question
          </Button>
        </>
      }
    >
      <Fieldset title="Question">
        <SelectField
          label="Type"
          value={type}
          onChange={(v) => {
            setType(v as QuestionType);
            // One answer for multiple choice; several for check-all.
            setCorrect(type === "single_choice" ? [] : correct);
          }}
          options={(Object.keys(QUESTION_TYPE_LABELS) as QuestionType[]).map((t) => ({
            value: t,
            label: QUESTION_TYPE_LABELS[t],
          }))}
          hint={QUESTION_TYPE_HINTS[type]}
        />
        <TextAreaField
          label="Prompt"
          value={prompt}
          onChange={setPrompt}
          rows={3}
          placeholder="What should the learner be asked?"
          autoFocus
        />
      </Fieldset>

      {hasOptions && (
        <Fieldset
          title="Options"
          description={
            type === "single_choice"
              ? "Tick the one correct answer."
              : "Tick every correct answer."
          }
        >
          {options.map((opt, i) => (
            <div className="option-row" key={i}>
              <input
                type={type === "single_choice" ? "radio" : "checkbox"}
                name="correct"
                checked={correct.includes(i)}
                onChange={() => {
                  if (type === "single_choice") setCorrect([i]);
                  else
                    setCorrect((c) =>
                      c.includes(i) ? c.filter((x) => x !== i) : [...c, i],
                    );
                }}
                aria-label={`Option ${i + 1} is correct`}
              />
              <input
                className="input"
                value={opt}
                onChange={(e) =>
                  setOptions((os) => os.map((o, j) => (j === i ? e.target.value : o)))
                }
                placeholder={`Option ${i + 1}`}
              />
              {options.length > 2 && (
                <button
                  className="icon-btn"
                  type="button"
                  aria-label={`Remove option ${i + 1}`}
                  onClick={() => {
                    setOptions((os) => os.filter((_, j) => j !== i));
                    setCorrect((c) =>
                      c.filter((x) => x !== i).map((x) => (x > i ? x - 1 : x)),
                    );
                  }}
                >
                  <Icon name="x" size={15} />
                </button>
              )}
            </div>
          ))}
          <Button
            size="sm"
            variant="secondary"
            icon={<Icon name="plus" size={14} />}
            onClick={() => setOptions((os) => [...os, ""])}
          >
            Add option
          </Button>
        </Fieldset>
      )}

      {hasLengths && (
        <Fieldset
          title="Answer limits"
          description={
            type === "short_text"
              ? "Short answers are usually one or two sentences."
              : "Long answers expect one or more paragraphs."
          }
        >
          <div className="field-row">
            <TextField
              label="Minimum characters"
              type="number"
              value={minLength}
              onChange={setMinLength}
            />
            <TextField
              label="Maximum characters"
              type="number"
              value={maxLength}
              onChange={setMaxLength}
            />
          </div>
        </Fieldset>
      )}

      <Fieldset title="Scoring">
        <TextField
          label="Points"
          type="number"
          value={points}
          onChange={setPoints}
          hint="Weighted by points when the attempt is scored."
        />
        <CheckboxField
          label="Required"
          hint="The learner cannot submit the attempt without answering this."
          checked={required}
          onChange={setRequired}
        />
        <TextAreaField
          label="Explanation"
          value={explanation}
          onChange={setExplanation}
          rows={2}
          placeholder="Shown to the learner after they submit, if you want."
        />
      </Fieldset>
    </Modal>
  );
}

/* ------------------------------------------------------------------ */
/* Resources: the uploaded source files                                */
/* ------------------------------------------------------------------ */

function ResourcesPanel({ lesson, mayEdit }: { lesson: Lesson; mayEdit: boolean }) {
  const assets = (lesson.blocks ?? [])
    .map((b) => b.asset)
    .filter((a): a is NonNullable<typeof a> => Boolean(a));

  if (assets.length === 0) {
    return (
      <div className="card">
        <EmptyState
          icon="download"
          title="No source files"
          text="Add a PDF, slide deck or image block and the uploaded file will be listed here."
        />
      </div>
    );
  }

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">Source files</div>
      </div>
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr>
              <th>File</th>
              <th>Type</th>
              <th>Pages</th>
              <th>Chapters</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {assets.map((a) => (
              <tr key={a.id}>
                <td className="cell-strong">{a.original_name}</td>
                <td className="small soft">{a.kind.toUpperCase()}</td>
                <td className="num">{a.page_count || "-"}</td>
                <td className="num">{a.chapter_count || "-"}</td>
                <td>
                  <Badge tone={assetTone(a.status)}>{a.status}</Badge>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {mayEdit && (
        <div className="card-footer small muted">
          Add or remove files from the Lesson tab.
        </div>
      )}
    </div>
  );
}

function assetTone(status: string): Tone {
  if (status === "ready") return "success";
  if (status === "failed") return "danger";
  if (status === "processing") return "warning";
  return "neutral";
}

export { formatPct };

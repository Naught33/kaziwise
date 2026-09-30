import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useApi } from "../lib/hooks";
import {
  BLOCK_TYPE_LABELS,
  formatDuration,
  formatPct,
  QUESTION_TYPE_HINTS,
  QUESTION_TYPE_LABELS,
} from "../lib/format";
import type {
  Assignment,
  Attempt,
  Block,
  Course,
  CourseOutline,
  LearnerDashboard,
  Lesson,
  Question,
  QuestionPaper,
} from "../lib/types";
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
  Spinner,
} from "../components/ui";
import { TextAreaField } from "../components/Field";
import { PdfViewer } from "../components/PdfViewer";

/**
 * The shape GET /v1/courses/{id}/play actually returns: the course and its
 * module/lesson tree are nested under "outline", not flattened alongside it.
 * The per-lesson state is a status string on each lesson ("", "in_progress"
 * or "completed") rather than a boolean.
 */
interface PlayPayload {
  course: Course;
  outline: CourseOutline;
  questions: Question[];
  assignment?: Assignment;
  learning_complete?: boolean;
  lessons_done?: number;
  lessons_total?: number;
  attempts_used?: number;
  max_attempts?: number;
}

/** A lesson plus the module it sits in, for the flat prev/next order. */
interface FlatLesson {
  moduleTitle: string;
  lesson: Lesson;
}

/** The API reports progress as a status string; the UI wants a boolean. */
function isComplete(lesson: Lesson): boolean {
  return lesson.status === "completed" || Boolean(lesson.completed_at);
}

/**
 * Course player.
 * Shows the current lesson and the learning outline, records page views for
 * PDF/deck blocks, and unlocks the assessment once the content is done.
 */
export default function CoursePlayer() {
  const { courseId = "" } = useParams();
  const nav = useNavigate();

  const play = useApi<PlayPayload>(
    (signal) => api.get<PlayPayload>(`/v1/courses/${courseId}/play`, { signal }),
    [courseId],
  );

  const dashboard = useApi<LearnerDashboard>(
    (signal) => api.get<LearnerDashboard>("/v1/dashboard", { signal }),
  );

  const [currentLessonId, setCurrentLessonId] = useState<string | null>(null);
  const [attemptOpen, setAttemptOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  // Flat lesson order for prev/next, read from the nested outline.
  const ordered = useMemo<FlatLesson[]>(() => {
    const out: FlatLesson[] = [];
    for (const m of play.data?.outline?.modules ?? []) {
      for (const l of m.lessons ?? []) {
        out.push({ moduleTitle: m.title, lesson: l });
      }
    }
    return out;
  }, [play.data]);

  // Progress and the assessment gate are decided by the server, which counts
  // only lessons that actually carry content. Deriving them from the outline
  // here would include assessment-only lessons that hold no blocks and can
  // never be marked complete, so the assessment could never unlock. The
  // outline is only a fallback for browsing a course with no assignment.
  //
  // The lessons that gate the assessment, i.e. the ones the server counts in
  // lessons_total. block_count > 0 mirrors the server's
  // "exists (select 1 from blocks ...)" so the two cannot disagree.
  const contentLessons = useMemo(
    () => ordered.filter((o) => o.lesson.block_count > 0),
    [ordered],
  );

  const doneCount = play.data?.lessons_done
    ?? contentLessons.filter((o) => isComplete(o.lesson)).length;
  const lessonTotal = play.data?.lessons_total ?? contentLessons.length;
  const progressPercent =
    lessonTotal > 0 ? (doneCount / lessonTotal) * 100 : 100;

  const allComplete =
    play.data?.learning_complete ??
    (ordered.length > 0 && ordered.every((o) => isComplete(o.lesson)));
  // The assessment belongs to the course, not to any one lesson, so it is
  // gated on progress alone. Keying it off the selected lesson's kind left
  // the button disabled on courses whose last lesson is plain content.
  const canAssess = allComplete;

  useEffect(() => {
    if (currentLessonId || ordered.length === 0) return;
    const firstIncomplete = ordered.find((o) => !isComplete(o.lesson));
    const first = (firstIncomplete ?? ordered[0]).lesson.id;
    setCurrentLessonId(first);
    // The learner list only knows the course id, so this is the first place
    // a real lesson id exists. Best effort: the player renders either way.
    api.post(`/v1/me/lessons/${first}/start`).catch(() => {});
  }, [ordered, currentLessonId]);

  const index = ordered.findIndex((o) => o.lesson.id === currentLessonId);
  const current = index >= 0 ? ordered[index] : null;
  // The lesson is already in the flattened list, so the separate lessons map
  // the old payload had is not needed.
  const lesson = current?.lesson ?? null;

  // The play payload carries the driving assignment when the learner has
  // one; fall back to the dashboard so the assessment card still shows.
  const assignment =
    play.data?.assignment ??
    (dashboard.data?.required_training ?? []).find((a) => a.course_id === courseId);

  async function markComplete(advance: boolean) {
    if (!currentLessonId) return;
    try {
      // Progress is a property of the assignment, not the lesson, so the
      // driving assignment id travels as a query param. The server rejects
      // the call without it because it cannot attribute the rollup.
      await api.post(`/v1/me/lessons/${currentLessonId}/complete`, undefined, {
        query: { assignment_id: assignment?.id },
      });
      setToast("Lesson complete.");
      play.reload();
      if (advance && index < ordered.length - 1) {
        setCurrentLessonId(ordered[index + 1].lesson.id);
      }
      setTimeout(() => setToast(null), 2500);
    } catch (err) {
      setToast(err instanceof ApiError ? err.message : "Could not save your progress.");
    }
  }

  // One stable handler per block id, so BlockView's effect does not
  // re-fire on every parent render. The cache is keyed by block id and
  // never cleared for the life of the player, which is fine: the closure
  // only captures the id.
  const pageHandlers = useRef(new Map<string, (page: number) => void>());
  const viewPageHandler = useCallback((block: Block) => {
    const cached = pageHandlers.current.get(block.id);
    if (cached) return cached;
    const fn = (page: number) => {
      void api
        .post(`/v1/courses/blocks/${block.id}/pages`, { pages: [page] })
        .catch(() => {
          // Progress tracking is best effort and must never interrupt the
          // lesson.
        });
    };
    pageHandlers.current.set(block.id, fn);
    return fn;
  }, []);

  if (play.initial) {
    return (
      <div className="card">
        <div className="card-body">
          <Skeleton height={300} />
        </div>
      </div>
    );
  }
  if (play.error) return <ErrorState error={play.error} onRetry={play.reload} />;
  if (!play.data) return null;

  return (
    <div className="player">
      {/* Outline */}
      <aside className="card player-outline">
        <div className="card-header">
          <div>
            <div className="card-title">{play.data.course.title}</div>
            <div className="card-sub">{formatPct(progressPercent)} complete</div>
          </div>
        </div>
        <div className="card-body card-body-flush">
          <ProgressBar value={progressPercent} showValue={false} />
          <ul className="player-tree">
            {(play.data.outline?.modules ?? []).map((m) => (
              <li key={m.id}>
                <div className="player-module">{m.title}</div>
                <ul>
                  {(m.lessons ?? []).map((l) => (
                    <li key={l.id}>
                      <button
                        type="button"
                        className={`player-lesson${l.id === currentLessonId ? " active" : ""}`}
                        onClick={() => setCurrentLessonId(l.id)}
                      >
                        <Icon
                          name={isComplete(l) ? "check" : "book"}
                          size={14}
                        />
                        <span>{l.title}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        </div>
      </aside>

      {/* Content */}
      <div className="player-main">
        {toast && <Alert tone="success">{toast}</Alert>}

        {lesson ? (
          <>
            <div className="card">
              <div className="card-header">
                <div>
                  <div className="card-sub">{current?.moduleTitle}</div>
                  <h2>{lesson.title}</h2>
                </div>
                {lesson.estimated_minutes > 0 && (
                  <Badge tone="neutral">
                    <Icon name="clock" size={13} /> {formatDuration(lesson.estimated_minutes)}
                  </Badge>
                )}
              </div>

              <div className="card-body lesson-body">
                {lesson.summary && <p className="soft" style={{ marginBottom: "var(--s4)" }}>{lesson.summary}</p>}

                {(lesson.blocks ?? []).length === 0 ? (
                  <EmptyState
                    icon="edit"
                    title="No content in this lesson"
                    text="Your administrator has not added anything here yet."
                  />
                ) : (
                  (lesson.blocks ?? []).map((b) => (
                    <BlockView
                      key={b.id}
                      block={b}
                      // Memoised per block: an inline lambda here is a new
                      // function every render, which re-fires any effect
                      // that depends on it and spams the page-view
                      // endpoint.
                      onViewPage={viewPageHandler(b)}
                    />
                  ))
                )}
              </div>

              <div className="card-footer">
                <div className="row-between">
                  <Button
                    variant="secondary"
                    disabled={index <= 0}
                    onClick={() => index > 0 && setCurrentLessonId(ordered[index - 1].lesson.id)}
                    icon={<Icon name="chevronLeft" size={15} />}
                  >
                    Previous
                  </Button>

                  {index === ordered.length - 1 ? (
                    canAssess || lesson?.kind === "assessment" ? (
                      <Button
                        onClick={() => setAttemptOpen(true)}
                        disabled={!allComplete}
                        icon={<Icon name="check" size={16} />}
                      >
                        {allComplete ? "Take the assessment" : "Finish the content first"}
                      </Button>
                    ) : (
                      <Button onClick={() => markComplete(false)}>Finish course</Button>
                    )
                  ) : (
                    <Button onClick={() => markComplete(true)}>
                      Mark Complete &amp; Continue
                    </Button>
                  )}
                </div>

                {index === ordered.length - 1 && !allComplete && (
                  <p className="small muted" style={{ marginTop: 8, marginBottom: 0 }}>
                    Complete the remaining lessons to unlock the assessment.
                  </p>
                )}
              </div>
            </div>

            {assignment && (
              <div className="card" style={{ marginTop: "var(--s4)" }}>
                <div className="card-body">
                  <div className="row-between">
                    <div>
                      <div className="card-title">Assessment</div>
                      <div className="card-sub">
                        {canAssess
                          ? "All lessons complete. The assessment is ready."
                          : "Available once you have marked every lesson complete."}
                      </div>
                    </div>
                    <Button
                      onClick={() => setAttemptOpen(true)}
                      disabled={!canAssess}
                      loading={false}
                    >
                      Start assessment
                    </Button>
                  </div>
                </div>
              </div>
            )}
          </>
        ) : (
          <div className="card">
            <EmptyState icon="book" title="Select a lesson" />
          </div>
        )}
      </div>

      {attemptOpen && assignment && (
        <AssessmentModal
          assignment={assignment}
          onClose={() => setAttemptOpen(false)}
          onFinished={() => {
            setAttemptOpen(false);
            play.reload();
            dashboard.reload();
            nav("/learn/training");
          }}
        />
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* One content block                                                   */
/* ------------------------------------------------------------------ */

function BlockView({ block, onViewPage }: { block: Block; onViewPage: (page: number) => void }) {
  return (
    <section className="block">
      {block.title && <h3 className="block-title">{block.title}</h3>}

      {block.type === "text" && (
        <div className="prose">
          {(block.body ?? "").split(/\n{2,}/).map((para, i) => (
            <p key={i}>{para}</p>
          ))}
        </div>
      )}

      {block.type === "video" && (
        <div>
          {block.asset?.download_url ? (
            <video controls preload="metadata" src={block.asset.download_url} className="video" />
          ) : block.url || block.body ? (
            <div className="video-frame">
              <a href={block.url ?? block.body} target="_blank" rel="noreferrer">
                Open the video
              </a>
            </div>
          ) : (
            <EmptyState icon="play" title="No video attached" text="Ask your administrator to add a video link." />
          )}
        </div>
      )}

      {block.type === "image" && (
        <div>
          {block.asset?.download_url || block.url ? (
            <img
              className="block-image"
              src={block.asset?.download_url ?? block.url}
              alt={block.title ?? "Course image"}
              loading="lazy"
            />
          ) : (
            <EmptyState icon="layers" title="No image attached" />
          )}
        </div>
      )}

      {(block.type === "pdf" || block.type === "pptx") && (
        <div>
          {block.asset?.download_url ? (
            <PdfViewer
              url={block.asset.download_url}
              fileName={block.asset.original_name}
              pages={block.pages ?? []}
              onViewPage={onViewPage}
            />
          ) : (
            <EmptyState
              icon="book"
              title="No document attached"
              text="This document is still being prepared. Try again in a moment."
            />
          )}
        </div>
      )}

      {block.type === "file" && block.asset && (
        <a className="btn btn-secondary" href={block.asset.download_url} download>
          <Icon name="download" size={15} /> {block.asset.original_name}
        </a>
      )}

      {block.type === "embed" && (block.url || block.body) && (
        <div className="video-frame">
          <a href={block.url ?? block.body} target="_blank" rel="noreferrer">
            Open the embedded page
          </a>
        </div>
      )}

      {block.type === "quiz" && (
        <Alert tone="info">
          Knowledge check in the lesson body. The scored assessment is at the end of the course.
        </Alert>
      )}

      <div className="block-kind">{BLOCK_TYPE_LABELS[block.type]}</div>
    </section>
  );
}

/* ------------------------------------------------------------------ */
/* Assessment                                                          */
/* ------------------------------------------------------------------ */

function AssessmentModal({
  assignment,
  onClose,
  onFinished,
}: {
  assignment: Assignment;
  onClose: () => void;
  onFinished: () => void;
}) {
  const [attempt, setAttempt] = useState<Attempt | null>(null);
  const [paper, setPaper] = useState<QuestionPaper | null>(null);
  const [answers, setAnswers] = useState<Record<string, string[]>>({});
  const [texts, setTexts] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<{ percent: number; passed: boolean; attempt: Attempt } | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function start() {
    setBusy(true);
    setError(null);
    try {
      const a = await api.post<Attempt>(
        `/v1/assignments/${assignment.id}/attempts`,
      );
      setAttempt(a);
      const p = await api.get<QuestionPaper>(`/v1/attempts/${a.id}/paper`);
      setPaper(p);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "The assessment could not be started.");
    } finally {
      setBusy(false);
    }
  }

  async function submit() {
    if (!attempt) return;
    setSubmitting(true);
    setError(null);
    try {
      // The endpoint takes a batch envelope, { answers: [...] }, so every
      // response goes up in one request instead of one round trip per
      // question.
      const payload: {
        question_id: string;
        selected_option_ids?: string[];
        response_text?: string;
      }[] = [];
      for (const [questionId, selected] of Object.entries(answers)) {
        if (selected.length === 0) continue;
        payload.push({ question_id: questionId, selected_option_ids: selected });
      }
      for (const [questionId, text] of Object.entries(texts)) {
        if (!text.trim()) continue;
        payload.push({ question_id: questionId, response_text: text });
      }
      if (payload.length > 0) {
        await api.post(`/v1/attempts/${attempt.id}/answers`, { answers: payload });
      }
      await api.post(`/v1/attempts/${attempt.id}/submit`);
      const res = await api.get<{ percent: number; passed: boolean }>(
        `/v1/attempts/${attempt.id}/result`,
      );
      setResult({ percent: res.percent, passed: res.passed, attempt });
      onFinished();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Your answers could not be submitted.");
    } finally {
      setSubmitting(false);
    }
  }

  if (result) {
    return (
      <Modal title="Assessment result" onClose={onFinished}>
        <div className={`result ${result.passed ? "result-pass" : "result-fail"}`}>
          <div className="result-icon" aria-hidden="true">
            <Icon name={result.passed ? "check" : "alert"} size={28} />
          </div>
          <div className="result-score">{formatPct(result.percent)}</div>
          <h2>{result.passed ? "You passed" : "Not passed this time"}</h2>
          <p className="soft">
            {result.passed
              ? "Your result has been recorded and a certificate has been issued."
              : `The pass mark is ${assignment.final_score !== undefined ? "" : ""}${
                  paper?.pass_mark ?? 80
                }%. Review the lessons and try again.`}
          </p>
        </div>
        <div style={{ marginTop: "var(--s4)" }}>
          <Button block onClick={onFinished}>
            Back to the course
          </Button>
        </div>
      </Modal>
    );
  }

  if (!paper) {
    return (
      <Modal
        title="Assessment"
        onClose={onClose}
        footer={
          <>
            <Button variant="secondary" onClick={onClose} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={start} loading={busy} disabled={busy}>
              Start assessment
            </Button>
          </>
        }
      >
        {error && <Alert tone="error">{error}</Alert>}
        <p>
          You are about to start the assessment for <strong>{assignment.course_title}</strong>.
        </p>
        <ul className="bullets">
          <li>Pass mark: {assignment.final_score !== undefined ? "" : ""}80%</li>
          <li>Attempts used: {assignment.attempts_used}</li>
          <li>You can retake it if you do not pass.</li>
        </ul>
      </Modal>
    );
  }

  // questions is read defensively: this component renders outside any error
  // boundary, so a payload without the list would blank the whole page
  // instead of showing an empty assessment.
  const questions = paper.questions ?? [];
  const totalPoints = paper.total_points || questions.length;

  return (
    <Modal title={paper.course_title} onClose={onClose} wide>
      {error && <Alert tone="error">{error}</Alert>}

      <div className="row-between" style={{ marginBottom: "var(--s4)" }}>
        <span className="small muted">
          {questions.length} question{questions.length === 1 ? "" : "s"} &middot;{" "}
          {totalPoints} points
        </span>
        <Badge tone="info">Pass mark {paper.pass_mark}%</Badge>
      </div>

      <div className="stack">
        {questions.map((q, i) => (
          <QuestionInput
            key={q.id}
            index={i + 1}
            question={q}
            selected={answers[q.id] ?? []}
            text={texts[q.id] ?? ""}
            onToggleOption={(optionId) =>
              setAnswers((a) => {
                const cur = a[q.id] ?? [];
                if (q.type === "single_choice") return { ...a, [q.id]: [optionId] };
                return {
                  ...a,
                  [q.id]: cur.includes(optionId)
                    ? cur.filter((x) => x !== optionId)
                    : [...cur, optionId],
                };
              })
            }
            onText={(v) => setTexts((t) => ({ ...t, [q.id]: v }))}
          />
        ))}
      </div>

      <div className="modal-footer" style={{ marginTop: "var(--s5)", padding: 0, background: "none", border: "none" }}>
        <Button variant="secondary" onClick={onClose} disabled={submitting}>
          Save and finish later
        </Button>
        <Button onClick={submit} loading={submitting} disabled={submitting}>
          {submitting ? "Submitting" : "Submit answers"}
        </Button>
      </div>
    </Modal>
  );
}

function QuestionInput({
  index,
  question,
  selected,
  text,
  onToggleOption,
  onText,
}: {
  index: number;
  question: Question;
  selected: string[];
  text: string;
  onToggleOption: (optionId: string) => void;
  onText: (v: string) => void;
}) {
  const hasOptions = question.type === "single_choice" || question.type === "multi_choice";
  const hasLength = question.type === "short_text" || question.type === "long_text";

  return (
    <fieldset className="question">
      <legend>
        <span className="question-num">{index}</span>
        {question.prompt}
        {question.is_required && <span className="question-required">required</span>}
      </legend>
      <div className="question-type">{QUESTION_TYPE_HINTS[question.type]}</div>

      {hasOptions && (
        <div className="options">
          {(question.options ?? []).map((o) => {
            const checked = selected.includes(o.id);
            return (
              <label className={`option${checked ? " checked" : ""}`} key={o.id}>
                <input
                  type={question.type === "single_choice" ? "radio" : "checkbox"}
                  name={`q-${question.id}`}
                  checked={checked}
                  onChange={() => onToggleOption(o.id)}
                />
                <span>{o.label}</span>
              </label>
            );
          })}
        </div>
      )}

      {hasLength && (
        <TextAreaField
          label={question.type === "short_text" ? "Your answer" : "Your explanation"}
          value={text}
          onChange={onText}
          rows={question.type === "long_text" ? 6 : 2}
          maxLength={question.max_length}
          showCount={Boolean(question.max_length)}
          placeholder={
            question.type === "short_text"
              ? "A short sentence or two."
              : "Explain in your own words. A paragraph is enough."
          }
        />
      )}

      <div className="question-foot small muted">
        {QUESTION_TYPE_LABELS[question.type]} &middot; {question.points} point
        {question.points === 1 ? "" : "s"}
        {question.max_length ? ` - up to ${question.max_length} characters` : ""}
      </div>
    </fieldset>
  );
}

export { Spinner };

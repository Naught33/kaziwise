import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useApi } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import {
  ASSIGNMENT_STATUS_LABELS,
  canViewReports,
  formatDate,
  formatDuration,
  formatPct,
  relativeTime,
  ROLE_LABELS,
} from "../lib/format";
import type { Assignment, LearnerDashboard } from "../lib/types";
import { PageHeader } from "../components/Layout";
import {
  Alert,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Icon,
  KpiCard,
  ProgressBar,
  Skeleton,
  type Tone,
} from "../components/ui";

/**
 * 08 - Learner View.
 * Welcome banner with name and overall progress, required training with due
 * dates, and certificates. Phone-first: the primary action sits high.
 */
export default function LearnerHome() {
  const { user } = useAuth();
  const nav = useNavigate();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const { data, loading, initial, error: loadError, reload } = useApi<LearnerDashboard>(
    (signal) => api.get<LearnerDashboard>("/v1/dashboard", { signal }),
  );

  async function start(a: Assignment) {
    setBusyId(a.id);
    setError(null);
    try {
      // No warm-up call here: this list only knows the course id, and
      // /me/lessons/{id}/start needs a lesson id. The player records the
      // start once it has resolved the first lesson.
      nav(`/learn/courses/${a.course_id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not open the course.");
    } finally {
      setBusyId(null);
    }
  }

  if (loadError && !initial) return <ErrorState error={loadError} onRetry={reload} />;

  const required = data?.required_training ?? [];
  const next =
    required.find((a) => a.status === "in_progress") ??
    required.find((a) => a.status === "not_started") ??
    required[0];

  return (
    <>
      {/* Welcome banner */}
      <section className="welcome">
        <div>
          <div className="welcome-greeting">
            {data?.greeting || `Welcome back, ${user?.full_name?.split(" ")[0] ?? ""}`}
          </div>
          <p className="welcome-sub">
            {next
              ? "Pick up where you left off, or start something new."
              : "You are all caught up on your required training."}
          </p>
        </div>
        {next && (
          <Button
            size="lg"
            icon={<Icon name="play" size={16} />}
            onClick={() => start(next)}
            loading={busyId === next.id}
            disabled={busyId === next.id}
            className="welcome-cta"
          >
            {next.status === "in_progress" ? "Continue Training" : "Start Training"}
          </Button>
        )}
      </section>

      {error && <Alert tone="error">{error}</Alert>}

      <div className="grid grid-kpi" style={{ marginBottom: "var(--s5)" }}>
        <KpiCard
          label="Overall progress"
          value={formatPct(data?.overall_progress_percent)}
          loading={loading && initial}
        />
        <KpiCard
          label="Assigned"
          value={data?.assigned_count ?? 0}
          hint={`${data?.in_progress_count ?? 0} in progress`}
          loading={loading && initial}
        />
        <KpiCard
          label="Passed"
          value={data?.passed_count ?? 0}
          tone="success"
          loading={loading && initial}
        />
        <KpiCard
          label="Overdue"
          value={data?.overdue_count ?? 0}
          tone={data && data.overdue_count > 0 ? "danger" : undefined}
          loading={loading && initial}
        />
      </div>

      {/* Required training */}
      <section className="card" style={{ marginBottom: "var(--s5)" }}>
        <div className="card-header">
          <div>
            <div className="card-title">Required Training</div>
            <div className="card-sub">
              {required.length} assigned course{required.length === 1 ? "" : "s"}
            </div>
          </div>
        </div>

        <div className="card-body card-body-flush">
          {loading && initial ? (
            <div style={{ padding: "var(--s5)" }} className="stack-sm">
              <Skeleton height={70} />
              <Skeleton height={70} />
            </div>
          ) : required.length ? (
            <ul className="task-list">
              {required.map((a) => (
                <li className="task-row" key={a.id}>
                  <div className={`task-icon task-${a.status}`} aria-hidden="true">
                    <Icon
                      name={
                        a.status === "passed"
                          ? "check"
                          : a.status === "failed" || a.status === "overdue"
                            ? "alert"
                            : "book"
                      }
                      size={18}
                    />
                  </div>

                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div className="row-between">
                      <div className="cell-strong">{a.course_title}</div>
                      <Badge tone={statusTone(a.status)}>
                        {ASSIGNMENT_STATUS_LABELS[a.status]}
                      </Badge>
                    </div>
                    {a.campaign_name && <div className="cell-sub">{a.campaign_name}</div>}

                    <div className="progress-wrap">
                      <ProgressBar value={a.progress_percent} />
                      <div className="cell-sub">
                        {a.lessons_done} of {a.lessons_total} lessons
                        {a.final_score !== undefined &&
                          ` - best score ${Math.round(a.best_score ?? 0)}%`}
                      </div>
                    </div>
                  </div>

                  <div className="task-due">
                    {a.due_date && (
                      <div className="small">
                        <span className="muted">Due </span>
                        <strong>{formatDate(a.due_date)}</strong>
                      </div>
                    )}
                    {a.status !== "passed" && (
                      <Button
                        size="sm"
                        onClick={() => start(a)}
                        loading={busyId === a.id}
                        disabled={busyId === a.id}
                      >
                        {a.status === "in_progress" ? "Continue" : "Start"}
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState
              icon="check"
              title="Nothing assigned yet"
              text="When your manager assigns training it will appear here with its deadline."
            />
          )}
        </div>
      </section>

      {/* Certificates summary */}
      <section className="card">
        <div className="card-header">
          <div className="card-title">My Certificates</div>
          <Button variant="ghost" size="sm" onClick={() => nav("/learn/certificates")}>
            View all
          </Button>
        </div>
        <div className="card-body card-body-flush">
          {loading && initial ? (
            <div style={{ padding: "var(--s5)" }}>
              <Skeleton height={60} />
            </div>
          ) : data && data.certificates?.length ? (
            <ul className="cert-mini-list">
              {data.certificates.slice(0, 4).map((c) => (
                <li key={c.id} className="cert-mini">
                  <div className="task-icon task-passed" aria-hidden="true">
                    <Icon name="award" size={18} />
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div className="cell-strong">{c.course_title}</div>
                    <div className="cell-sub">
                      Score {formatPct(c.score)} &middot; {formatDate(c.completed_at)}
                    </div>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => window.open(c.render_url ?? `/certificates/${c.id}`, "_blank")}
                  >
                    View
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState
              icon="award"
              title="No certificates yet"
              text="Pass an assessment with certificates enabled and your certificate appears here."
            />
          )}
        </div>
      </section>
    </>
  );
}

/** The full list of assigned training, for the Learning tab. */
export function MyTraining() {
  const nav = useNavigate();

  // The full list, passed assignments included, is the paginated assignment
  // endpoint: the dashboard view only carries outstanding required training.
  // per_page is explicit because the endpoint defaults to 20, and this screen
  // has always shown everything assigned to the learner.
  const { data, error, loading, initial, reload } = useApi<Assignment[]>(
    (signal) => api.get<Assignment[]>("/v1/me/training", { signal, query: { per_page: 100 } }),
  );

  if (error && !initial) return <ErrorState error={error} onRetry={reload} />;

  const all = data ?? [];
  const outstanding = all.filter((a) => a.status !== "passed");
  const done = all.filter((a) => a.status === "passed");

  return (
    <>
      <PageHeader title="My Training" subtitle="Everything assigned to you, in one place." />

      {loading && initial ? (
        <div className="card">
          <div className="card-body stack-sm">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} height={64} />
            ))}
          </div>
        </div>
      ) : all.length === 0 ? (
        <div className="card">
          <EmptyState
            icon="book"
            title="No training assigned"
            text="Your manager has not assigned any courses yet. Check back after the next campaign launch."
          />
        </div>
      ) : (
        <div className="stack">
          {outstanding.length > 0 && (
            <div className="card">
              <div className="card-header">
                <div className="card-title">To do</div>
              </div>
              <ul className="task-list">
                {outstanding.map((a) => (
                  <LearningRow key={a.id} a={a} onOpen={nav} />
                ))}
              </ul>
            </div>
          )}

          {done.length > 0 && (
            <div className="card">
              <div className="card-header">
                <div className="card-title">Completed</div>
              </div>
              <ul className="task-list">
                {done.map((a) => (
                  <LearningRow key={a.id} a={a} onOpen={nav} />
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </>
  );
}

function LearningRow({ a, onOpen }: { a: Assignment; onOpen: (to: string) => void }) {
  return (
    <li className="task-row">
      <div className={`task-icon task-${a.status}`} aria-hidden="true">
        <Icon name={a.status === "passed" ? "check" : "book"} size={18} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="row-between">
          <div className="cell-strong">{a.course_title}</div>
          <Badge tone={statusTone(a.status)}>{ASSIGNMENT_STATUS_LABELS[a.status]}</Badge>
        </div>
        <div className="cell-sub">
          {a.lessons_done}/{a.lessons_total} lessons
          {a.due_date && ` - due ${formatDate(a.due_date)}`}
          {` - last activity ${relativeTime(a.last_activity_at)}`}
        </div>
      </div>
      <Button
        size="sm"
        variant={a.status === "passed" ? "secondary" : "primary"}
        onClick={() => onOpen(`/learn/courses/${a.course_id}`)}
      >
        {a.status === "passed" ? "Review" : a.status === "in_progress" ? "Continue" : "Start"}
      </Button>
    </li>
  );
}

/**
 * Account profile. The learner progress cards only make sense for a
 * training account, so they are hidden for staff, who reach this screen
 * from the account menu rather than the bottom navigation.
 */
export function Profile() {
  const { user, org } = useAuth();
  const nav = useNavigate();
  const learner = !canViewReports(user?.role);
  const { data } = useApi<LearnerDashboard>(
    (signal) => api.get<LearnerDashboard>("/v1/dashboard", { signal }),
    [learner],
  );

  return (
    <>
      <PageHeader title="Profile" />

      <div className="card" style={{ marginBottom: "var(--s4)" }}>
        <div className="card-body">
          <div className="row" style={{ gap: "var(--s4)" }}>
            <div className="avatar avatar-lg" style={{ width: 56, height: 56, fontSize: 20 }}>
              {(user?.full_name ?? "")
                .split(" ")
                .filter(Boolean)
                .map((p) => p[0])
                .slice(0, 2)
                .join("")
                .toUpperCase()}
            </div>
            <div style={{ minWidth: 0 }}>
              <h2>{user?.full_name}</h2>
              <div className="soft small">{user?.email}</div>
              <div className="row-wrap" style={{ marginTop: 8 }}>
                <Badge tone="primary">{org?.name ?? "KaziWise"}</Badge>
                {user?.role && <Badge tone="neutral">{ROLE_LABELS[user.role]}</Badge>}
                {user?.department && <Badge tone="neutral">{user.department}</Badge>}
                {user?.job_title && <Badge tone="neutral">{user.job_title}</Badge>}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div className={`grid ${learner ? "grid-3" : "grid-2"}`}>
        {learner && (
          <>
            <div className="card">
              <div className="card-body">
                <div className="card-sub">Overall progress</div>
                <div className="kpi-value">{formatPct(data?.overall_progress_percent)}</div>
                <ProgressBar value={data?.overall_progress_percent} />
              </div>
            </div>
            <div className="card">
              <div className="card-body">
                <div className="card-sub">Certificates earned</div>
                <div className="kpi-value">{data?.certificates?.length ?? 0}</div>
                <Button size="sm" variant="ghost" onClick={() => nav("/learn/certificates")}>
                  View certificates
                </Button>
              </div>
            </div>
          </>
        )}
        <div className="card">
          <div className="card-body">
            <div className="card-sub">Security</div>
            <div className="small soft" style={{ marginBottom: 8 }}>
              {user?.must_reset_password
                ? "Your password must be changed before you continue."
                : "Change your password if you have shared it."}
            </div>
            <Button size="sm" variant="secondary" onClick={() => nav("/change-password")}>
              Change password
            </Button>
          </div>
        </div>
      </div>
    </>
  );
}

function statusTone(s: Assignment["status"]): Tone {
  if (s === "passed") return "success";
  if (s === "failed" || s === "overdue") return "danger";
  if (s === "in_progress") return "primary";
  if (s === "pending_review") return "warning";
  return "neutral";
}

export { formatDuration };

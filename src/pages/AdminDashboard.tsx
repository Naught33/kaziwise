import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { useApi } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import { formatDate, formatPct, firstName, pct } from "../lib/format";
import type { Dashboard } from "../lib/types";
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
  TableSkeleton,
  type Tone,
} from "../components/ui";

/**
 * 01 - Admin Dashboard.
 * High-level training KPIs, active campaigns and attention items, plus the
 * primary "+ Create Campaign" action from the prototype.
 */
export default function AdminDashboard() {
  const { user, org } = useAuth();
  const nav = useNavigate();
  // /v1/dashboard is the learner view; the staff dashboard is /dashboard/admin.
  const { data, error, initial, reload } = useApi<Dashboard>(
    (signal) => api.get<Dashboard>("/v1/dashboard/admin", { signal }),
  );

  const kpis = data?.kpis;

  return (
    <>
      <PageHeader
        title={`Good day, ${firstName(user?.full_name) || "there"}`}
        subtitle={
          org
            ? `Training health for ${org.name}. ${data ? `Updated ${formatDate(data.generated_at)}.` : ""}`
            : "Training health across your organisation."
        }
        actions={
          <>
            <Button variant="secondary" icon={<Icon name="chart" size={16} />} onClick={() => nav("/reports")}>
              View Reports
            </Button>
            {/* One clear primary action per screen. */}
            <Button icon={<Icon name="plus" size={16} />} onClick={() => nav("/campaigns?new=1")}>
              Create Campaign
            </Button>
          </>
        }
      />

      {error && !initial && (
        <Alert tone="error" title="Dashboard unavailable">
          {error.message}
          <div style={{ marginTop: 8 }}>
            <Button size="sm" variant="secondary" onClick={reload}>
              Try again
            </Button>
          </div>
        </Alert>
      )}

      {/* KPI cards: Employees, Active Courses, Completion, Pass Rate */}
      <div className="grid grid-kpi" style={{ marginBottom: "var(--s5)" }}>
        <KpiCard
          label="Employees"
          value={kpis?.active_employees ?? 0}
          hint={
            kpis ? `${kpis.employees} total across ${kpis.departments} departments` : undefined
          }
          loading={initial}
          onClick={() => nav("/employees")}
        />
        <KpiCard
          label="Active Courses"
          value={kpis?.published_courses ?? 0}
          hint={kpis ? `${kpis.draft_courses} in draft` : undefined}
          loading={initial}
          onClick={() => nav("/courses")}
        />
        <KpiCard
          label="Completion"
          value={formatPct(kpis?.completion_percent)}
          hint={kpis ? `${kpis.completed} of ${kpis.assigned} assignments` : undefined}
          tone="primary"
          loading={initial}
          onClick={() => nav("/reports")}
        />
        <KpiCard
          label="Pass Rate"
          value={formatPct(kpis?.pass_rate_percent)}
          hint={
            kpis
              ? kpis.average_score !== undefined
                ? `Average score ${Math.round(kpis.average_score)}%`
                : `${kpis.passed} passed`
              : undefined
          }
          tone={pct(kpis?.pass_rate_percent) >= 80 ? "success" : "warning"}
          loading={initial}
          onClick={() => nav("/reports")}
        />
      </div>

      <div className="dash-grid">
        {/* Active campaigns: assigned population, completion, progress bar */}
        <section className="card">
          <div className="card-header">
            <div>
              <div className="card-title">Active Campaigns</div>
              <div className="card-sub">{kpis?.active_campaigns ?? 0} running now</div>
            </div>
            <Button variant="ghost" size="sm" onClick={() => nav("/campaigns")}>
              All campaigns
            </Button>
          </div>
          <div className="card-body card-body-flush">
            {initial ? (
              <div style={{ padding: "var(--s5)" }}>
                <Skeleton height={70} />
              </div>
            ) : data?.active_campaigns.length ? (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Campaign</th>
                      <th>Assigned</th>
                      <th style={{ width: "30%" }}>Completion</th>
                      <th>Due</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.active_campaigns.map((c) => (
                      <tr key={c.id}>
                        <td>
                          <div className="cell-strong">{c.name}</div>
                          <div className="cell-sub">{c.course_title}</div>
                        </td>
                        <td className="num">{c.assigned_count}</td>
                        <td>
                          <ProgressBar value={c.progress_percent} />
                        </td>
                        <td className="small soft">{formatDate(c.due_date)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <EmptyState
                icon="megaphone"
                title="No campaigns are running"
                text="Create a campaign to assign a published course to a department or the whole company."
                action={
                  <Button size="sm" onClick={() => nav("/campaigns?new=1")}>
                    Create Campaign
                  </Button>
                }
              />
            )}
          </div>
        </section>

        {/* Attention required: overdue, failed, upcoming deadlines */}
        <section className="card">
          <div className="card-header">
            <div className="card-title">Attention Required</div>
            {kpis && (kpis.overdue > 0 || kpis.failed > 0 || kpis.pending_review > 0) && (
              <Badge tone="danger">
                {kpis.overdue + kpis.failed + kpis.pending_review}
              </Badge>
            )}
          </div>
          <div className="card-body card-body-flush">
            {initial ? (
              <div style={{ padding: "var(--s5)" }} className="stack-sm">
                <Skeleton height={44} />
                <Skeleton height={44} />
                <Skeleton height={44} />
              </div>
            ) : data?.attention_required.length ? (
              <ul>
                {data.attention_required.slice(0, 8).map((item, i) => (
                  <li className="attention-row" key={`${item.learner_id}-${item.course_title}-${i}`}>
                    <Badge tone={attentionTone(item.type)}>{attentionLabel(item.type)}</Badge>
                    <div style={{ minWidth: 0, flex: 1 }}>
                      <div className="cell-strong">{item.learner_name}</div>
                      <div className="cell-sub">
                        {item.course_title} &middot; {item.detail}
                      </div>
                    </div>
                    {item.due_date && (
                      <span className="small muted nowrap">{formatDate(item.due_date)}</span>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState
                icon="check"
                title="Nothing needs your attention"
                text="No overdue learners, failed assessments or pending reviews right now."
              />
            )}
          </div>
        </section>
      </div>

      {/* Department progress */}
      <section className="card" style={{ marginTop: "var(--s5)" }}>
        <div className="card-header">
          <div className="card-title">Progress by Department</div>
        </div>
        <div className="card-body card-body-flush">
          {initial ? (
            <div style={{ padding: "var(--s5)" }}>
              <TableSkeleton rows={3} cols={4} />
            </div>
          ) : data?.department_progress.length ? (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>Department</th>
                    <th>Headcount</th>
                    <th>Assigned</th>
                    <th style={{ width: "28%" }}>Completion</th>
                    <th>Pass rate</th>
                    <th>Overdue</th>
                  </tr>
                </thead>
                <tbody>
                  {data.department_progress.map((d) => (
                    <tr key={d.department || "unassigned"}>
                      <td className="cell-strong">{d.department || "Unassigned"}</td>
                      <td className="num">{d.headcount}</td>
                      <td className="num">{d.assigned}</td>
                      <td>
                        <ProgressBar value={d.completion_percent} />
                      </td>
                      <td className="num">{formatPct(d.pass_rate_percent)}</td>
                      <td>
                        {d.overdue > 0 ? (
                          <Badge tone="danger">{d.overdue} overdue</Badge>
                        ) : (
                          <span className="muted small">-</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <EmptyState
              icon="users"
              title="No department data yet"
              text="Add employees and assign a department to see progress broken down by team."
              action={
                <Button size="sm" onClick={() => nav("/employees")}>
                  Go to Employees
                </Button>
              }
            />
          )}
        </div>
      </section>

      {error && initial && <ErrorState error={error} onRetry={reload} />}
    </>
  );
}

function attentionTone(type: string): Tone {
  switch (type) {
    case "overdue":
      return "danger";
    case "failed":
      return "danger";
    case "due_soon":
      return "warning";
    case "pending_review":
      return "info";
    default:
      return "neutral";
  }
}

function attentionLabel(type: string): string {
  switch (type) {
    case "overdue":
      return "Overdue";
    case "failed":
      return "Failed";
    case "due_soon":
      return "Due soon";
    case "pending_review":
      return "To review";
    default:
      return type;
  }
}

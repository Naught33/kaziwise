import { useMemo, useState } from "react";
import { api, ApiError, downloadFile } from "../lib/api";
import { useApi, useDebounced } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import {
  ASSIGNMENT_STATUS_LABELS,
  canViewReports,
  formatDate,
  formatPct,
  relativeTime,
} from "../lib/format";
import type { Assignment, Campaign, ReportSummary, StatusBreakdown } from "../lib/types";
import { PageHeader } from "../components/Layout";
import {
  Alert,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Icon,
  KpiCard,
  Modal,
  Skeleton,
  TableSkeleton,
  type Tone,
} from "../components/ui";

const STATUS_ORDER: Assignment["status"][] = [
  "not_started",
  "in_progress",
  "pending_review",
  "passed",
  "failed",
  "overdue",
];

/** 06 - Training Reports. Assigned, started, completed, passed, failed, overdue. */
export default function Reports() {
  const { user } = useAuth();
  const mayView = canViewReports(user?.role);

  const [campaignId, setCampaignId] = useState("");
  const [department, setDepartment] = useState("");
  const [status, setStatus] = useState("");
  const [search, setSearch] = useState("");
  const [onlyMyTeam, setOnlyMyTeam] = useState(false);
  const [exporting, setExporting] = useState<string | null>(null);
  const [exportError, setExportError] = useState<string | null>(null);
  const debouncedSearch = useDebounced(search);

  const campaigns = useApi<Campaign[]>((signal) => api.get<Campaign[]>("/v1/campaigns", { signal }));

  const filters = useMemo(
    () => ({
      campaign_id: campaignId || undefined,
      department: department || undefined,
      status: status || undefined,
      search: debouncedSearch || undefined,
      only_my_team: onlyMyTeam || undefined,
    }),
    [campaignId, department, status, debouncedSearch, onlyMyTeam],
  );

  const report = useApi<ReportSummary>(
    (signal) => api.get<ReportSummary>("/v1/reports/overview", { signal, query: filters }),
    [filters],
  );

  const rows = useApi<Assignment[]>(
    (signal) => api.get<Assignment[]>("/v1/reports/rows", { signal, query: filters }),
    [filters],
  );

  const departmentNames = useMemo(() => {
    const names = new Set<string>();
    for (const d of report.data?.by_department ?? []) {
      if (d.department) names.add(d.department);
    }
    for (const r of rows.data ?? []) {
      if (r.department) names.add(r.department);
    }
    return [...names].sort();
  }, [report.data, rows.data]);

  async function exportAs(kind: "csv" | "xlsx" | "pdf") {
    setExporting(kind);
    setExportError(null);
    try {
      // The pdf route returns a printable HTML report and the xlsx route
      // returns a spreadsheet-compatible HTML file, so name them as such.
      const fallback = kind === "csv" ? "csv" : kind === "xlsx" ? "xls" : "html";
      await downloadFile(
        `/v1/reports/export/${kind}`,
        `kaziwise-training-report.${fallback}`,
      );
    } catch (err) {
      setExportError(err instanceof ApiError ? err.message : "The export could not be created.");
    } finally {
      setExporting(null);
    }
  }

  if (!mayView) {
    return (
      <div className="card">
        <EmptyState
          icon="lock"
          title="Reports are for managers and administrators"
          text="Ask an administrator if you need a training report for your team."
        />
      </div>
    );
  }

  const breakdown = report.data?.breakdown;

  return (
    <>
      <PageHeader
        title="Training Reports"
        subtitle="Who has been assigned, started, completed and passed, and who is overdue."
        actions={
          <>
            <Button
              variant="secondary"
              icon={<Icon name="download" size={16} />}
              onClick={() => exportAs("csv")}
              loading={exporting === "csv"}
              disabled={exporting !== null}
            >
              Download CSV
            </Button>
            <Button
              variant="secondary"
              icon={<Icon name="download" size={16} />}
              onClick={() => exportAs("xlsx")}
              loading={exporting === "xlsx"}
              disabled={exporting !== null}
            >
              Download Excel
            </Button>
            <Button
              icon={<Icon name="download" size={16} />}
              onClick={() => exportAs("pdf")}
              loading={exporting === "pdf"}
              disabled={exporting !== null}
              title="Opens a printable HTML report"
            >
              Download Report (HTML)
            </Button>
          </>
        }
      />

      {exportError && <Alert tone="error">{exportError}</Alert>}

      {/* Summary cards: Assigned, Started, Completed, Passed */}
      <div className="grid grid-kpi" style={{ marginBottom: "var(--s5)" }}>
        <KpiCard
          label="Assigned"
          value={breakdown?.total ?? 0}
          hint={`${breakdown?.not_started ?? 0} not started`}
          loading={report.initial}
        />
        <KpiCard
          label="Started"
          value={breakdown?.in_progress ?? 0}
          tone="primary"
          loading={report.initial}
        />
        <KpiCard
          label="Completed"
          value={breakdown?.passed ?? 0}
          tone="success"
          hint={breakdown ? formatPct(breakdown.pass_rate_percent) + " pass rate" : undefined}
          loading={report.initial}
        />
        <KpiCard
          label="Overdue"
          value={breakdown?.overdue ?? 0}
          tone={breakdown && breakdown.overdue > 0 ? "danger" : undefined}
          hint={breakdown ? `${breakdown.failed ?? 0} failed` : undefined}
          loading={report.initial}
        />
      </div>

      {/* Filters */}
      <div className="card" style={{ marginBottom: "var(--s4)" }}>
        <div className="card-body">
          <div className="filter-bar">
            <div className="search-box">
              <Icon name="search" size={16} />
              <input
                className="input"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search by name or email"
                aria-label="Search learners"
              />
            </div>
            <select
              className="select"
              value={campaignId}
              onChange={(e) => setCampaignId(e.target.value)}
              aria-label="Filter by campaign"
              style={{ maxWidth: 220 }}
            >
              <option value="">All campaigns</option>
              {(campaigns.data ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <select
              className="select"
              value={department}
              onChange={(e) => setDepartment(e.target.value)}
              aria-label="Filter by department"
              style={{ maxWidth: 180 }}
            >
              <option value="">All departments</option>
              {departmentNames.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </select>
            <select
              className="select"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              aria-label="Filter by status"
              style={{ maxWidth: 170 }}
            >
              <option value="">All statuses</option>
              {STATUS_ORDER.map((s) => (
                <option key={s} value={s}>
                  {ASSIGNMENT_STATUS_LABELS[s]}
                </option>
              ))}
            </select>
            <label className="checkbox" style={{ minHeight: "auto", marginBottom: 0 }}>
              <input
                type="checkbox"
                checked={onlyMyTeam}
                onChange={(e) => setOnlyMyTeam(e.target.checked)}
              />
              <span className="checkbox-text small">Only my team</span>
            </label>
            {(campaignId || department || status || search || onlyMyTeam) && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setCampaignId("");
                  setDepartment("");
                  setStatus("");
                  setSearch("");
                  setOnlyMyTeam(false);
                }}
              >
                Clear
              </Button>
            )}
          </div>
        </div>
      </div>

      {report.error && !report.initial ? (
        <ErrorState error={report.error} onRetry={report.reload} />
      ) : (
        <>
          {/* Status breakdown */}
          <div className="card" style={{ marginBottom: "var(--s4)" }}>
            <div className="card-header">
              <div className="card-title">Status breakdown</div>
              {report.data && (
                <span className="small muted">
                  Average score {Math.round(report.data.breakdown.average_score_percent)}%
                </span>
              )}
            </div>
            <div className="card-body">
              {report.initial ? (
                <Skeleton height={40} />
              ) : breakdown ? (
                <StatusBar breakdown={breakdown} />
              ) : (
                <span className="muted small">No data.</span>
              )}
            </div>
          </div>

          {/* Learner-level table */}
          <div className="card">
            <div className="card-header">
              <div className="card-title">Learners</div>
              <span className="small muted">
                {rows.data?.length ?? 0} record{(rows.data?.length ?? 0) === 1 ? "" : "s"}
              </span>
            </div>

            {rows.initial ? (
              <div className="card-body">
                <TableSkeleton rows={6} cols={5} />
              </div>
            ) : rows.data && rows.data.length ? (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Employee</th>
                      <th>Course</th>
                      <th>Status</th>
                      <th>Progress</th>
                      <th>Score</th>
                      <th>Last activity</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {rows.data.map((r) => (
                      <tr key={r.id}>
                        <td>
                          <div className="cell-strong">{r.learner_name}</div>
                          <div className="cell-sub">
                            {r.department || "Unassigned"}
                            {r.learner_email ? ` - ${r.learner_email}` : ""}
                          </div>
                        </td>
                        <td className="small">
                          {r.course_title}
                          {r.campaign_name && <div className="cell-sub">{r.campaign_name}</div>}
                        </td>
                        <td>
                          <Badge tone={statusTone(r.status)}>
                            {ASSIGNMENT_STATUS_LABELS[r.status]}
                          </Badge>
                        </td>
                        <td>
                          <div className="small tabular">
                            {r.lessons_done}/{r.lessons_total} lessons
                          </div>
                        </td>
                        <td className="num">
                          {r.final_score !== undefined ? `${Math.round(r.final_score)}%` : "-"}
                        </td>
                        <td className="small soft nowrap">
                          {relativeTime(r.last_activity_at)}
                          {r.due_date && (
                            <div className="cell-sub">Due {formatDate(r.due_date)}</div>
                          )}
                        </td>
                        <td>
                          <div className="td-actions">
                            <ViewButton row={r} />
                            {r.status !== "passed" && (
                              <Button
                                size="sm"
                                variant="ghost"
                                onClick={async () => {
                                  try {
                                    await api.post(`/v1/assignments/${r.id}/remind`);
                                    alert(`Reminder sent to ${r.learner_name}.`);
                                  } catch (err) {
                                    alert(
                                      err instanceof ApiError
                                        ? err.message
                                        : "The reminder could not be sent.",
                                    );
                                  }
                                }}
                              >
                                Remind
                              </Button>
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <EmptyState
                icon="chart"
                title="No records match"
                text="Adjust the filters above, or assign a campaign to start collecting training data."
              />
            )}
          </div>

          {/* By department */}
          {report.data && report.data.by_department.length > 0 && (
            <div className="card" style={{ marginTop: "var(--s4)" }}>
              <div className="card-header">
                <div className="card-title">By department</div>
              </div>
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Department</th>
                      <th>Headcount</th>
                      <th>Assigned</th>
                      <th>Completed</th>
                      <th>Passed</th>
                      <th>Overdue</th>
                      <th>Pass rate</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.data.by_department.map((d) => (
                      <tr key={d.department || "unassigned"}>
                        <td className="cell-strong">{d.department || "Unassigned"}</td>
                        <td className="num">{d.headcount}</td>
                        <td className="num">{d.assigned}</td>
                        <td className="num">{d.completed}</td>
                        <td className="num">{d.passed}</td>
                        <td className="num">
                          {d.overdue > 0 ? <Badge tone="danger">{d.overdue}</Badge> : "0"}
                        </td>
                        <td className="num">{formatPct(d.pass_rate_percent)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}
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

/** Proportional bar; the numbers are printed alongside it, never colour only. */
function StatusBar({ breakdown }: { breakdown: StatusBreakdown }) {
  const total = breakdown.total || 1;
  const segments = STATUS_ORDER.map((s) => ({
    status: s,
    count: breakdown[s],
  })).filter((seg) => seg.count > 0);

  return (
    <div className="stack-sm">
      <div className="status-bar" role="img" aria-label="Assignment status breakdown">
        {segments.map((seg) => (
          <div
            key={seg.status}
            className={`status-seg seg-${seg.status}`}
            style={{ width: `${(seg.count / total) * 100}%` }}
            title={`${ASSIGNMENT_STATUS_LABELS[seg.status]}: ${seg.count}`}
          />
        ))}
      </div>
      <div className="row-wrap small">
        {segments.map((seg) => (
          <span className="row" key={seg.status} style={{ gap: 6 }}>
            <span className={`legend-dot seg-${seg.status}`} aria-hidden="true" />
            <span className="muted">{ASSIGNMENT_STATUS_LABELS[seg.status]}</span>
            <strong className="tabular">{seg.count}</strong>
          </span>
        ))}
      </div>
    </div>
  );
}

/** 07 - Certificate detail, also used from the learner view. */
function ViewButton({ row }: { row: Assignment }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" variant="ghost" onClick={() => setOpen(true)}>
        View
      </Button>
      {open && <AssignmentModal row={row} onClose={() => setOpen(false)} />}
    </>
  );
}

function AssignmentModal({ row, onClose }: { row: Assignment; onClose: () => void }) {
  const detail = useApi<{ attempts: unknown[]; certificates: unknown[] }>(
    (signal) => api.get(`/v1/assignments/${row.id}`, { signal }),
    [row.id],
  );

  return (
    <Modal title={row.learner_name ?? "Learner"} onClose={onClose}>
      <div className="stack">
        <div>
          <div className="card-sub">Course</div>
          <div className="cell-strong">{row.course_title}</div>
          {row.campaign_name && <div className="cell-sub">{row.campaign_name}</div>}
        </div>

        <div className="grid grid-3">
          <Mini label="Status" value={ASSIGNMENT_STATUS_LABELS[row.status]} />
          <Mini label="Progress" value={`${Math.round(row.progress_percent)}%`} />
          <Mini
            label="Best score"
            value={row.best_score !== undefined ? `${Math.round(row.best_score)}%` : "-"}
          />
        </div>

        <div>
          <div className="card-sub">Timeline</div>
          <ul className="timeline">
            <li>
              <span className="muted">Assigned</span> {formatDate(row.assigned_at)}
            </li>
            {row.started_at && (
              <li>
                <span className="muted">Started</span> {formatDate(row.started_at)}
              </li>
            )}
            {row.completed_at && (
              <li>
                <span className="muted">Completed</span> {formatDate(row.completed_at)}
              </li>
            )}
            {row.passed_at && (
              <li>
                <span className="muted">Passed</span> {formatDate(row.passed_at)}
              </li>
            )}
            {row.due_date && (
              <li>
                <span className="muted">Due</span> {formatDate(row.due_date)}
              </li>
            )}
            <li>
              <span className="muted">Attempts</span> {row.attempts_used}
            </li>
          </ul>
        </div>

        {detail.error && (
          <Alert tone="info">
            Detailed attempt history is unavailable: {detail.error.message}
          </Alert>
        )}
      </div>
      <div style={{ marginTop: "var(--s4)" }}>
        <Button block onClick={onClose}>
          Close
        </Button>
      </div>
    </Modal>
  );
}

function Mini({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="card-sub">{label}</div>
      <div style={{ fontWeight: 650, color: "var(--nav)" }}>{value}</div>
    </div>
  );
}

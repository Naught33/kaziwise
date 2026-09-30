import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useApi } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import {
  AUDIENCE_LABELS,
  canManage,
  CAMPAIGN_STATUS_LABELS,
  formatDate,
  formatPct,
  isStaff,
} from "../lib/format";
import type { Assignment, Campaign, CampaignStatus, Course, DepartmentProgress } from "../lib/types";
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
  TableSkeleton,
  type Tone,
} from "../components/ui";
import { CheckboxField, Fieldset, SelectField, TextAreaField, TextField } from "../components/Field";

/** 05 - Training Campaigns. Assign courses to groups with deadlines and pass marks. */
export default function Campaigns() {
  const { user } = useAuth();
  const nav = useNavigate();
  const mayEdit = canManage(user?.role);
  const [params, setParams] = useSearchParams();

  const [createOpen, setCreateOpen] = useState(params.get("new") === "1");
  const [selected, setSelected] = useState<Campaign | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const campaigns = useApi<Campaign[]>((signal) =>
    api.get<Campaign[]>("/v1/campaigns", { signal }),
  );

  // Clear the ?new=1 flag so a refresh does not reopen the modal.
  useEffect(() => {
    if (params.get("new")) {
      params.delete("new");
      setParams(params, { replace: true });
    }
  }, [params, setParams]);

  async function act(c: Campaign, key: "launch" | "close" | "remind") {
    setBusyId(c.id);
    setActionError(null);
    setNotice(null);
    try {
      if (key === "launch") await api.post(`/v1/campaigns/${c.id}/launch`);
      else if (key === "close") await api.post(`/v1/campaigns/${c.id}/close`);
      else await api.post(`/v1/campaigns/${c.id}/remind`);
      setNotice(
        key === "remind"
          ? "Reminder queued for everyone who has not finished."
          : key === "launch"
            ? "Campaign launched. Learners have been assigned."
            : "Campaign closed.",
      );
      campaigns.reload();
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "That action could not be completed.");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <>
      <PageHeader
        title="Training Campaigns"
        subtitle="Assign a course to a department or the whole company, then track it to completion."
        actions={
          mayEdit && (
            <Button icon={<Icon name="plus" size={16} />} onClick={() => setCreateOpen(true)}>
              Create Campaign
            </Button>
          )
        }
      />

      {actionError && <Alert tone="error">{actionError}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}

      {campaigns.error && !campaigns.initial ? (
        <ErrorState error={campaigns.error} onRetry={campaigns.reload} />
      ) : campaigns.initial ? (
        <TableSkeleton rows={5} cols={5} />
      ) : campaigns.data && campaigns.data.length ? (
        <div className="card">
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Campaign</th>
                  <th>Audience</th>
                  <th>Status</th>
                  <th style={{ width: "22%" }}>Completion</th>
                  <th>Passed</th>
                  <th>Overdue</th>
                  <th>Deadline</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {campaigns.data.map((c) => (
                  <tr key={c.id}>
                    <td>
                      <div className="cell-strong">{c.name}</div>
                      <div className="cell-sub">
                        {c.course_title} &middot; pass mark {c.pass_mark}%
                      </div>
                    </td>
                    <td className="small">
                      {AUDIENCE_LABELS[c.audience_type] ?? c.audience_type}
                      {c.audience_department && (
                        <div className="cell-sub">{c.audience_department}</div>
                      )}
                    </td>
                    <td>
                      <Badge tone={campaignTone(c.status)}>
                        {CAMPAIGN_STATUS_LABELS[c.status]}
                      </Badge>
                    </td>
                    <td>
                      <ProgressBar value={c.progress_percent} />
                      <div className="cell-sub">
                        {c.completed_count} of {c.assigned_count} completed
                      </div>
                    </td>
                    <td className="num">
                      {c.passed_count}
                      {c.failed_count > 0 && (
                        <div className="cell-sub" style={{ color: "var(--danger)" }}>
                          {c.failed_count} failed
                        </div>
                      )}
                    </td>
                    <td className="num">
                      {c.overdue_count > 0 ? (
                        <Badge tone="danger">{c.overdue_count}</Badge>
                      ) : (
                        <span className="muted">0</span>
                      )}
                    </td>
                    <td className="small soft nowrap">{formatDate(c.due_date)}</td>
                    <td>
                      <div className="td-actions">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setSelected(c)}
                        >
                          View
                        </Button>
                        {mayEdit && c.status === "draft" && (
                          <Button
                            size="sm"
                            onClick={() => act(c, "launch")}
                            loading={busyId === c.id}
                            disabled={busyId === c.id}
                          >
                            Launch
                          </Button>
                        )}
                        {mayEdit && c.status === "active" && (
                          <>
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() => act(c, "remind")}
                              disabled={busyId === c.id}
                            >
                              Remind
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => act(c, "close")}
                              disabled={busyId === c.id}
                            >
                              Close
                            </Button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="card">
          <EmptyState
            icon="megaphone"
            title="No campaigns yet"
            text="A campaign assigns a published course to people and starts tracking their progress."
            action={
              mayEdit ? (
                <Button size="sm" onClick={() => setCreateOpen(true)}>
                  Create Campaign
                </Button>
              ) : undefined
            }
          />
        </div>
      )}

      {createOpen && (
        <CreateCampaignModal
          onClose={() => setCreateOpen(false)}
          onSaved={() => {
            setCreateOpen(false);
            campaigns.reload();
          }}
        />
      )}

      {selected && (
        <CampaignDetailModal
          campaign={selected}
          isStaff={isStaff(user?.role)}
          onClose={() => setSelected(null)}
          onChanged={campaigns.reload}
        />
      )}

      {!mayEdit && (
        <p className="small muted" style={{ marginTop: "var(--s4)" }}>
          Managers can view campaigns and remind their team, but only administrators can create or
          launch one.{" "}
          <button className="link-back" onClick={() => nav("/reports")} type="button">
            Open reports
          </button>
        </p>
      )}
    </>
  );
}

function campaignTone(s: CampaignStatus): Tone {
  if (s === "active") return "success";
  if (s === "draft") return "warning";
  return "neutral";
}

interface DepartmentOption {
  name?: string;
  department?: string;
}

/** Create then optionally launch, so the flow matches the prototype steps. */
function CreateCampaignModal({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState({
    name: "",
    course_id: "",
    audience_type: "all",
    audience_department: "",
    due_date: "",
    pass_mark: "80",
    issue_certificate: true,
    max_attempts: "2",
    require_learning: true,
    description: "",
  });
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"draft" | "launch" | null>(null);

  const courses = useApi<Course[]>((signal) =>
    api.get<Course[]>("/v1/courses", { signal, query: { status: "published" } }),
  );
  const departments = useApi<DepartmentOption[]>((signal) =>
    api.get<DepartmentOption[]>("/v1/org/departments", { signal }),
  );

  const deptNames = useMemo(() => {
    const raw = departments.data ?? [];
    return raw
      .map((d) => d.name ?? d.department ?? "")
      .filter(Boolean)
      .sort();
  }, [departments.data]);

  const set = (k: keyof typeof form) => (v: string) =>
    setForm((f) => ({ ...f, [k]: v }));

  const published = (courses.data ?? []).filter((c) => c.status === "published");

  async function submit(mode: "draft" | "launch") {
    setError(null);
    setFields({});
    setBusy(mode);
    try {
      const created = await api.post<Campaign>("/v1/campaigns", {
        name: form.name.trim(),
        course_id: form.course_id,
        audience_type: form.audience_type,
        ...(form.audience_type === "department" && form.audience_department
          ? { audience_department: form.audience_department }
          : {}),
        ...(form.due_date ? { due_date: new Date(form.due_date).toISOString() } : {}),
        pass_mark: Number(form.pass_mark) || 80,
        issue_certificate: form.issue_certificate,
        max_attempts: Number(form.max_attempts) || 1,
        require_learning: form.require_learning,
        ...(form.description.trim() ? { description: form.description.trim() } : {}),
      });
      if (mode === "launch") await api.post(`/v1/campaigns/${created.id}/launch`);
      onSaved();
    } catch (err) {
      if (err instanceof ApiError) {
        setFields(err.fieldMessages);
        setError(err.message);
      } else {
        setError("Cannot reach the KaziWise server.");
      }
      setBusy(null);
    }
  }

  function submitForm(e: FormEvent) {
    e.preventDefault();
    void submit("launch");
  }

  return (
    <Modal
      title="Create training campaign"
      wide
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={() => submit("draft")} disabled={busy !== null}>
            {busy === "draft" ? "Saving" : "Save Draft"}
          </Button>
          <Button onClick={submitForm} loading={busy === "launch"} disabled={busy !== null}>
            {busy === "launch" ? "Launching" : "Launch Campaign"}
          </Button>
        </>
      }
    >
      {error && <Alert tone="error">{error}</Alert>}

      {courses.initial ? (
        <Skeleton height={200} />
      ) : published.length === 0 ? (
        <Alert tone="info" title="No published courses">
          A campaign can only assign a published course. Publish a course in the Course Library
          first, then come back here.
        </Alert>
      ) : (
        <form onSubmit={submitForm} noValidate>
          <Fieldset
            title="Campaign"
            description="The name identifies this training event in reports and reminders."
          >
            <TextField
              label="Campaign name"
              value={form.name}
              onChange={set("name")}
              placeholder="2026 Safety Induction"
              error={fields.name}
              required
              autoFocus
            />
            <SelectField
              label="Course"
              value={form.course_id}
              onChange={set("course_id")}
              placeholder="Select a published course"
              options={published.map((c) => ({
                value: c.id,
                label: `${c.title} (${c.lesson_count} lessons)`,
              }))}
              error={fields.course_id}
              required
            />
            <TextAreaField
              label="Description"
              value={form.description}
              onChange={set("description")}
              rows={2}
              placeholder="Optional note shown to learners."
            />
          </Fieldset>

          <Fieldset title="Assign to">
            <SelectField
              label="Audience"
              value={form.audience_type}
              onChange={set("audience_type")}
              options={[
                { value: "all", label: "Entire company" },
                { value: "department", label: "A department" },
              ]}
              error={fields.audience_type}
            />
            {form.audience_type === "department" && (
              <SelectField
                label="Department"
                value={form.audience_department}
                onChange={set("audience_department")}
                placeholder="Select a department"
                options={deptNames.map((d) => ({ value: d, label: d }))}
                error={fields.audience_department}
                required
              />
            )}
            <TextField
              label="Deadline"
              type="date"
              value={form.due_date}
              onChange={set("due_date")}
              error={fields.due_date}
              hint="After this date, unfinished training shows as overdue."
            />
          </Fieldset>

          <Fieldset
            title="Passing rules"
            description="Learners must reach the pass mark to pass and, if enabled, receive a certificate."
          >
            <TextField
              label="Pass mark (%)"
              type="number"
              value={form.pass_mark}
              onChange={set("pass_mark")}
              error={fields.pass_mark}
            />
            <TextField
              label="Maximum attempts"
              type="number"
              value={form.max_attempts}
              onChange={set("max_attempts")}
              error={fields.max_attempts}
              hint="How many times a learner may sit the assessment."
            />
            <CheckboxField
              label="Issue a certificate on pass"
              checked={form.issue_certificate}
              onChange={(v) => setForm((f) => ({ ...f, issue_certificate: v }))}
            />
            <CheckboxField
              label="Require the learning content first"
              hint="The assessment only unlocks after every lesson is marked complete."
              checked={form.require_learning}
              onChange={(v) => setForm((f) => ({ ...f, require_learning: v }))}
            />
          </Fieldset>
        </form>
      )}
    </Modal>
  );
}

function CampaignDetailModal({
  campaign,
  isStaff,
  onClose,
  onChanged,
}: {
  campaign: Campaign;
  isStaff: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const audience = useApi<{ learners: Assignment[] }>(
    (signal) => api.get<{ learners: Assignment[] }>(`/v1/campaigns/${campaign.id}/audience`, { signal }),
    [campaign.id],
  );

  async function remind(assignmentId: string) {
    setBusy(true);
    setError(null);
    try {
      await api.post(`/v1/assignments/${assignmentId}/remind`);
      audience.reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "The reminder could not be sent.");
    } finally {
      setBusy(false);
    }
  }

  const learners = audience.data?.learners ?? [];

  return (
    <Modal title={campaign.name} onClose={onClose} wide>
      {error && <Alert tone="error">{error}</Alert>}

      <div className="grid grid-3" style={{ marginBottom: "var(--s5)" }}>
        <Stat label="Assigned" value={campaign.assigned_count} />
        <Stat label="Completed" value={campaign.completed_count} />
        <Stat label="Passed" value={campaign.passed_count} tone="success" />
        <Stat label="Failed" value={campaign.failed_count} tone={campaign.failed_count ? "danger" : undefined} />
        <Stat label="Overdue" value={campaign.overdue_count} tone={campaign.overdue_count ? "danger" : undefined} />
        <Stat label="Pass mark" value={`${campaign.pass_mark}%`} />
      </div>

      <div className="row-wrap" style={{ marginBottom: "var(--s4)" }}>
        <Badge tone="info">Pass mark {campaign.pass_mark}%</Badge>
        <Badge tone="neutral">Max {campaign.max_attempts} attempts</Badge>
        {campaign.issue_certificate && <Badge tone="success">Certificates on pass</Badge>}
        {campaign.require_learning && <Badge tone="neutral">Content required first</Badge>}
        {campaign.due_date && <Badge tone="warning">Due {formatDate(campaign.due_date)}</Badge>}
      </div>

      {audience.initial ? (
        <TableSkeleton rows={4} cols={4} />
      ) : learners.length === 0 ? (
        <EmptyState
          icon="users"
          title="Nobody assigned yet"
          text="Launch this campaign to create the assignments for its audience."
        />
      ) : (
        <div className="table-wrap" style={{ maxHeight: 340, overflowY: "auto" }}>
          <table className="table">
            <thead>
              <tr>
                <th>Learner</th>
                <th>Status</th>
                <th style={{ width: "26%" }}>Progress</th>
                <th>Score</th>
                {isStaff && <th />}
              </tr>
            </thead>
            <tbody>
              {learners.map((a) => (
                <tr key={a.id}>
                  <td>
                    <div className="cell-strong">{a.learner_name}</div>
                    <div className="cell-sub">{a.department || "Unassigned"}</div>
                  </td>
                  <td>
                    <Badge
                      tone={
                        a.status === "passed"
                          ? "success"
                          : a.status === "failed" || a.status === "overdue"
                            ? "danger"
                            : a.status === "in_progress"
                              ? "primary"
                              : "neutral"
                      }
                    >
                      {a.status.replace(/_/g, " ")}
                    </Badge>
                  </td>
                  <td>
                    <ProgressBar value={a.progress_percent} />
                    <div className="cell-sub">
                      {a.lessons_done} of {a.lessons_total} lessons
                    </div>
                  </td>
                  <td className="num">
                    {a.final_score !== undefined ? `${Math.round(a.final_score)}%` : "-"}
                  </td>
                  {isStaff && (
                    <td>
                      <div className="td-actions">
                        {a.status !== "passed" && (
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => remind(a.id)}
                            disabled={busy}
                          >
                            Remind
                          </Button>
                        )}
                      </div>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="modal-footer" style={{ marginTop: "var(--s4)", padding: 0, background: "none", border: "none" }}>
        <Button variant="secondary" onClick={onChanged}>
          Refresh
        </Button>
        <Button onClick={onClose}>Close</Button>
      </div>
    </Modal>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | string;
  tone?: "success" | "danger";
}) {
  return (
    <div className="card">
      <div className="card-body" style={{ padding: "var(--s3) var(--s4)" }}>
        <div className="card-sub">{label}</div>
        <div
          className="kpi-value"
          style={tone ? { color: `var(--${tone})` } : undefined}
        >
          {value}
        </div>
      </div>
    </div>
  );
}

export { formatPct, type DepartmentProgress };

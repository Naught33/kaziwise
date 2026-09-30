import { useMemo, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "../lib/api";
import { useApi, useDebounced } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import {
  canManage,
  formatPct,
  relativeTime,
  ROLE_LABELS,
  USER_STATUS_LABELS,
} from "../lib/format";
import type { ImportResult, User, UserStatus } from "../lib/types";
import { PageHeader } from "../components/Layout";
import {
  Alert,
  Avatar,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Icon,
  Modal,
  ProgressBar,
  TableSkeleton,
  type Tone,
} from "../components/ui";
import { Fieldset, SelectField, TextField } from "../components/Field";

/** 02 - Employees. Search, department filter, add and import. */
export default function Employees() {
  const { user } = useAuth();
  const mayEdit = canManage(user?.role);

  const [search, setSearch] = useState("");
  const [department, setDepartment] = useState("");
  const [status, setStatus] = useState("");
  const debounced = useDebounced(search);

  const [addOpen, setAddOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);

  const query = useMemo(
    () => ({
      search: debounced || undefined,
      department: department || undefined,
      status: status || undefined,
      per_page: 100,
    }),
    [debounced, department, status],
  );

  const list = useApi<User[]>(
    (signal) => api.page<User>("/v1/employees", query, signal).then((p) => p.items),
    [query],
  );

  const departments = useApi<string[]>(
    (signal) =>
      api
        .get<{ departments: { name: string }[] } | { name: string }[]>("/v1/org/departments", {
          signal,
        })
        .then((res) => {
          // The endpoint has changed shape before; accept both.
          if (Array.isArray(res)) return res.map((d) => (d as { name: string }).name);
          return (res as { departments: { name: string }[] }).departments.map((d) => d.name);
        }),
    [],
  );

  const deptNames = useMemo(() => {
    const names = departments.data ?? [];
    // Include any department present in the filtered rows so a filter never
    // silently hides the only option that could widen it again.
    for (const u of list.data ?? []) {
      if (u.department && !names.includes(u.department)) names.push(u.department);
    }
    return names.sort();
  }, [departments.data, list.data]);

  return (
    <>
      <PageHeader
        title="Employees"
        subtitle="Everyone in your organisation and how their training is tracking."
        actions={
          mayEdit && (
            <>
              <Button variant="secondary" icon={<Icon name="upload" size={16} />} onClick={() => setImportOpen(true)}>
                Import CSV
              </Button>
              <Button icon={<Icon name="plus" size={16} />} onClick={() => setAddOpen(true)}>
                Add Employee
              </Button>
            </>
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
                placeholder="Search by name, email or employee number"
                aria-label="Search employees"
              />
            </div>
            <select
              className="select"
              value={department}
              onChange={(e) => setDepartment(e.target.value)}
              aria-label="Filter by department"
              style={{ maxWidth: 200 }}
            >
              <option value="">All departments</option>
              {deptNames.map((d) => (
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
              style={{ maxWidth: 160 }}
            >
              <option value="">All statuses</option>
              <option value="active">Active</option>
              <option value="invited">Invited</option>
              <option value="inactive">Inactive</option>
            </select>
            {(search || department || status) && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setSearch("");
                  setDepartment("");
                  setStatus("");
                }}
              >
                Clear
              </Button>
            )}
          </div>
        </div>
      </div>

      {list.error && !list.initial ? (
        <ErrorState error={list.error} onRetry={list.reload} />
      ) : list.initial ? (
        <TableSkeleton rows={6} cols={6} />
      ) : list.data && list.data.length ? (
        <div className="card">
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Employee</th>
                  <th>Employee no.</th>
                  <th>Department</th>
                  <th>Role</th>
                  <th>Status</th>
                  <th style={{ width: 200 }}>Training progress</th>
                  <th>Last seen</th>
                </tr>
              </thead>
              <tbody>
                {list.data.map((u) => (
                  <tr key={u.id}>
                    <td>
                      <div className="row" style={{ gap: 10 }}>
                        <Avatar name={u.full_name} />
                        <div style={{ minWidth: 0 }}>
                          <div className="cell-strong">{u.full_name}</div>
                          <div className="cell-sub">{u.email}</div>
                        </div>
                      </div>
                    </td>
                    <td className="num small soft">{u.employee_number || "-"}</td>
                    <td className="small">{u.department || <span className="muted">Unassigned</span>}</td>
                    <td>
                      <Badge tone={u.role === "learner" ? "neutral" : "primary"} plain>
                        {ROLE_LABELS[u.role]}
                      </Badge>
                    </td>
                    <td>
                      <Badge tone={userStatusTone(u.status)}>{USER_STATUS_LABELS[u.status]}</Badge>
                    </td>
                    <td>
                      {u.assigned_count ? (
                        <>
                          <ProgressBar value={u.progress_percent} />
                          <div className="cell-sub">
                            {u.completed_count ?? 0} of {u.assigned_count} completed
                          </div>
                        </>
                      ) : (
                        <span className="muted small">
                          {u.role === "learner" ? "No training assigned" : "Not applicable"}
                        </span>
                      )}
                    </td>
                    <td className="small soft nowrap">{relativeTime(u.last_seen_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="card-footer small muted">
            {list.data.length} employee{list.data.length === 1 ? "" : "s"}
            {department && ` in ${department}`}
            {search && ` matching "${search}"`}
          </div>
        </div>
      ) : (
        <div className="card">
          <EmptyState
            icon="users"
            title={
              search || department || status
                ? "No employees match those filters"
                : "No employees yet"
            }
            text={
              search || department || status
                ? "Try a different search term, or clear the filters to see everyone."
                : "Add employees one at a time, or import a CSV to bring in your whole list at once."
            }
            action={
              mayEdit && !search && !department && !status ? (
                <div className="row">
                  <Button size="sm" onClick={() => setAddOpen(true)}>
                    Add Employee
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setImportOpen(true)}>
                    Import CSV
                  </Button>
                </div>
              ) : search || department || status ? (
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    setSearch("");
                    setDepartment("");
                    setStatus("");
                  }}
                >
                  Clear filters
                </Button>
              ) : undefined
            }
          />
        </div>
      )}

      {addOpen && (
        <AddEmployeeModal
          departments={deptNames}
          onClose={() => setAddOpen(false)}
          onSaved={() => {
            setAddOpen(false);
            list.reload();
            departments.reload();
          }}
        />
      )}

      {importOpen && (
        <ImportModal
          onClose={() => setImportOpen(false)}
          onDone={() => {
            list.reload();
            departments.reload();
          }}
        />
      )}
    </>
  );
}

function userStatusTone(s: UserStatus): Tone {
  if (s === "active") return "success";
  if (s === "invited") return "warning";
  return "neutral";
}

function AddEmployeeModal({
  departments,
  onClose,
  onSaved,
}: {
  departments: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState({
    full_name: "",
    email: "",
    role: "learner",
    department: "",
    employee_number: "",
    job_title: "",
    status: "active",
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
      await api.post<User>("/v1/employees", {
        full_name: form.full_name.trim(),
        email: form.email.trim(),
        role: form.role,
        ...(form.department.trim() ? { department: form.department.trim() } : {}),
        ...(form.employee_number.trim() ? { employee_number: form.employee_number.trim() } : {}),
        ...(form.job_title.trim() ? { job_title: form.job_title.trim() } : {}),
        status: form.status,
      });
      onSaved();
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
      title="Add employee"
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={busy}>
            Add employee
          </Button>
        </>
      }
    >
      {error && <Alert tone="error">{error}</Alert>}
      <form onSubmit={submit} noValidate>
        <Fieldset title="Identity">
          <TextField
            label="Full name"
            value={form.full_name}
            onChange={set("full_name")}
            error={fields.full_name}
            required
            autoFocus
          />
          <TextField
            label="Work email"
            type="email"
            value={form.email}
            onChange={set("email")}
            error={fields.email}
            placeholder="name@company.com"
            required
          />
        </Fieldset>

        <Fieldset title="Work details">
          <div className="field-row">
            <SelectField
              label="Role"
              value={form.role}
              onChange={set("role")}
              options={[
                { value: "learner", label: "Learner" },
                { value: "manager", label: "Manager" },
                { value: "org_admin", label: "Organisation Admin" },
              ]}
              hint="Managers see their team's progress. Admins manage everything."
              error={fields.role}
            />
            <SelectField
              label="Status"
              value={form.status}
              onChange={set("status")}
              options={[
                { value: "active", label: "Active" },
                { value: "invited", label: "Invited" },
                { value: "inactive", label: "Inactive" },
              ]}
              error={fields.status}
            />
          </div>
          {/* Existing departments are offered, but any new one can be typed. */}
          <TextField
            label="Department"
            value={form.department}
            onChange={set("department")}
            error={fields.department}
            placeholder="Operations"
            list={departments.length > 0 ? "known-departments" : undefined}
          />
          {departments.length > 0 && (
            <datalist id="known-departments">
              {departments.map((d) => (
                <option key={d} value={d} />
              ))}
            </datalist>
          )}
          <div className="field-row">
            <TextField
              label="Employee number"
              value={form.employee_number}
              onChange={set("employee_number")}
              error={fields.employee_number}
            />
            <TextField
              label="Job title"
              value={form.job_title}
              onChange={set("job_title")}
              error={fields.job_title}
            />
          </div>
        </Fieldset>
      </form>
    </Modal>
  );
}

const CSV_TEMPLATE = `full_name,email,role,department,employee_number,job_title
Amina Yusuf,amina@acme.com,learner,Operations,AC-001,Safety Officer
Peter Mensah,peter@acme.com,manager,Field Ops,AC-002,Field Supervisor
`;

/** CSV import, matching the backend's column names. */
function ImportModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function upload() {
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      const form = new FormData();
      form.append("file", file);
      const res = await api.upload<ImportResult>("/v1/employees/import", form);
      setResult(res);
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "The upload could not be read.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="Import employees"
      onClose={onClose}
      footer={
        result ? (
          <Button onClick={onClose}>Done</Button>
        ) : (
          <>
            <Button variant="secondary" onClick={onClose} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={upload} loading={busy} disabled={busy || !file}>
              Import {file ? file.name : "file"}
            </Button>
          </>
        )
      }
    >
      {error && <Alert tone="error">{error}</Alert>}

      {result ? (
        <>
          <Alert tone={result.failed > 0 ? "info" : "success"} title="Import finished">
            {result.created} created, {result.updated} updated, {result.skipped} skipped,{" "}
            {result.failed} failed out of {result.total_rows} rows.
          </Alert>
          {result.errors.length > 0 && (
            <div className="alert alert-error">
              <div className="alert-body">
                <div className="alert-title">Rows that need attention</div>
                <ul>
                  {result.errors.map((e, i) => (
                    <li key={i}>{e}</li>
                  ))}
                </ul>
              </div>
            </div>
          )}
        </>
      ) : (
        <>
          <p className="soft" style={{ marginBottom: "var(--s4)" }}>
            Upload a CSV with one employee per row. The first line must be a header row.
          </p>
          <div className="field">
            <span className="field-label">CSV file</span>
            <input
              ref={fileRef}
              className="input"
              type="file"
              accept=".csv,text/csv"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
            <span className="field-hint">
              Columns: full_name, email, role, department, employee_number, job_title. Only
              full_name and email are required.
            </span>
          </div>
          <details>
            <summary className="small" style={{ cursor: "pointer", color: "var(--primary)" }}>
              Show an example file
            </summary>
            <pre className="code-block">{CSV_TEMPLATE}</pre>
          </details>
        </>
      )}
    </Modal>
  );
}

export { formatPct };

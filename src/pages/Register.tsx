import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { ApiError } from "../lib/api";
import { useAuth, type RegisterPayload } from "../lib/auth";
import { Alert, Button } from "../components/ui";
import { Fieldset, SelectField, TextField } from "../components/Field";

/**
 * Registration. The backend decides the final role: a founder who creates
 * an organisation becomes its admin, and an invite code overrides whatever
 * the form says. So the role here is a hint, not a guarantee, and the copy
 * says so.
 */
export default function Register() {
  const { register, user, loading } = useAuth();
  const nav = useNavigate();

  const [form, setForm] = useState({
    full_name: "",
    email: "",
    password: "",
    confirm: "",
    org_name: "",
    invite_code: "",
    job_title: "",
    department: "",
  });
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!loading && user) return <Navigate to="/" replace />;

  const set = (k: keyof typeof form) => (v: string) => setForm((f) => ({ ...f, [k]: v }));

  const joiningViaInvite = form.invite_code.trim().length > 0;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setFields({});

    if (form.password !== form.confirm) {
      setFields({ confirm: "The two passwords do not match." });
      return;
    }

    setBusy(true);
    const payload: RegisterPayload = {
      full_name: form.full_name.trim(),
      email: form.email.trim(),
      password: form.password,
    };
    if (joiningViaInvite) payload.invite_code = form.invite_code.trim();
    else if (form.org_name.trim()) payload.org_name = form.org_name.trim();
    if (form.job_title.trim()) payload.job_title = form.job_title.trim();
    if (form.department.trim()) payload.department = form.department.trim();

    try {
      await register(payload);
      nav("/", { replace: true });
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
    <div className="auth">
      <div className="auth-panel auth-panel-wide">
        <div className="brand" style={{ marginBottom: "var(--s5)" }}>
          <div className="brand-mark">KW</div>
          <div>
            <div className="brand-name">KaziWise</div>
            <div className="brand-tag">Train. Track. Improve.</div>
          </div>
        </div>

        <h1>Create your account</h1>
        <p className="soft" style={{ marginTop: 6, marginBottom: "var(--s5)" }}>
          Join an existing organisation with an invite code, or set up a new one and become its
          administrator.
        </p>

        {error && <Alert tone="error">{error}</Alert>}

        <form onSubmit={submit} noValidate>
          <Fieldset title="Your details">
            <TextField
              label="Full name"
              value={form.full_name}
              onChange={set("full_name")}
              autoComplete="name"
              error={fields.full_name}
              required
              autoFocus
            />
            <TextField
              label="Work email"
              type="email"
              value={form.email}
              onChange={set("email")}
              autoComplete="email"
              placeholder="you@company.com"
              error={fields.email}
              required
            />
            <div className="field-row">
              <TextField
                label="Job title"
                value={form.job_title}
                onChange={set("job_title")}
                error={fields.job_title}
              />
              <TextField
                label="Department"
                value={form.department}
                onChange={set("department")}
                error={fields.department}
              />
            </div>
          </Fieldset>

          <Fieldset
            title={joiningViaInvite ? "Your invitation" : "Your organisation"}
            description={
              joiningViaInvite
                ? "Your role and department come from the invitation."
                : "You will be the first administrator of this organisation."
            }
          >
            {joiningViaInvite ? (
              <TextField
                label="Invite code"
                value={form.invite_code}
                onChange={set("invite_code")}
                error={fields.invite_code}
                hint="Paste the code from your invitation email."
                required
              />
            ) : (
              <TextField
                label="Organisation name"
                value={form.org_name}
                onChange={set("org_name")}
                placeholder="Acme Ltd"
                error={fields.org_name}
                hint="You can change this later in settings."
              />
            )}
          </Fieldset>

          <Fieldset title="Password">
            <TextField
              label="Password"
              type="password"
              value={form.password}
              onChange={set("password")}
              autoComplete="new-password"
              error={fields.password}
              hint="At least 10 characters, with a letter and a number."
              required
            />
            <TextField
              label="Confirm password"
              type="password"
              value={form.confirm}
              onChange={set("confirm")}
              autoComplete="new-password"
              error={fields.confirm}
              required
            />
          </Fieldset>

          <Button type="submit" size="lg" block loading={busy} disabled={busy}>
            {busy ? "Creating your account" : "Create account"}
          </Button>
        </form>

        <p className="small muted" style={{ marginTop: "var(--s5)" }}>
          Already have an account? <Link to="/login">Sign in</Link>
        </p>
      </div>
    </div>
  );
}

/** Accept an invite: same as register but pre-focused on the code. */
export function AcceptInvite() {
  const [code, setCode] = useState("");
  return (
    <div className="auth">
      <div className="auth-panel">
        <h1>Accept your invitation</h1>
        <p className="soft" style={{ marginTop: 6, marginBottom: "var(--s5)" }}>
          Enter the code from your invitation email to join the training programme.
        </p>
        <TextField
          label="Invite code"
          value={code}
          onChange={setCode}
          autoFocus
          required
        />
        <Button
          size="lg"
          block
          onClick={() => {
            window.location.href = `/register?invite=${encodeURIComponent(code)}`;
          }}
        >
          Continue
        </Button>
        <p className="small muted" style={{ marginTop: "var(--s5)" }}>
          <Link to="/login">Back to sign in</Link>
        </p>
      </div>
    </div>
  );
}

export { SelectField };

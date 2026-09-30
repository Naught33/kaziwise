import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Alert, Button, Icon, Spinner } from "../components/ui";
import { TextField } from "../components/Field";

/**
 * Sign-in screen.
 *
 * When one email exists in more than one organisation the API answers
 * 409 org_selection_required. Rather than guessing a tenant, we ask for
 * the organisation code and retry.
 */
export default function Login() {
  const { login, user, loading } = useAuth();
  const nav = useNavigate();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [orgSlug, setOrgSlug] = useState("");
  const [candidates, setCandidates] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!loading && user) return <Navigate to="/" replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await login(email.trim(), password, orgSlug.trim() || undefined);
      nav("/", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        // Ask which tenant, and remember the candidate list.
        if (err.code === "org_selection_required") {
          setCandidates(err.organisations ?? []);
          setError(
            "That email address exists in more than one organisation. Enter your organisation code.",
          );
        } else {
          setError(err.message);
        }
      } else {
        setError("Cannot reach the KaziWise server. Check that the API is running.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth">
      <div className="auth-panel">
        <div className="brand" style={{ marginBottom: "var(--s6)" }}>
          <div className="brand-mark">KW</div>
          <div>
            <div className="brand-name">KaziWise</div>
            <div className="brand-tag">Train. Track. Improve.</div>
          </div>
        </div>

        <h1>Sign in</h1>
        <p className="soft" style={{ marginTop: 6, marginBottom: "var(--s5)" }}>
          Employee training and compliance for growing organisations.
        </p>

        {error && <Alert tone="error">{error}</Alert>}

        {candidates && candidates.length > 0 && (
          <div className="org-picker">
            <div className="field-label" style={{ marginBottom: 8 }}>
              Your organisation
            </div>
            <div className="org-picker-list">
              {candidates.map((slug) => (
                <button
                  key={slug}
                  type="button"
                  className={`org-chip${orgSlug === slug ? " active" : ""}`}
                  onClick={() => {
                    setOrgSlug(slug);
                    setCandidates(null);
                    setError(null);
                  }}
                >
                  {slug}
                </button>
              ))}
            </div>
            <p className="field-hint" style={{ marginTop: 8 }}>
              Or type the code below.
            </p>
          </div>
        )}

        <form onSubmit={submit} noValidate>
          <TextField
            label="Work email"
            type="email"
            value={email}
            onChange={setEmail}
            autoComplete="username"
            placeholder="you@company.com"
            required
            autoFocus
          />
          <TextField
            label="Password"
            type="password"
            value={password}
            onChange={setPassword}
            autoComplete="current-password"
            placeholder="Your password"
            required
          />
          <TextField
            label="Organisation code"
            value={orgSlug}
            onChange={setOrgSlug}
            placeholder="Leave blank unless asked"
            hint="Only needed if your email is used by more than one organisation."
          />

          <Button type="submit" size="lg" block loading={busy} disabled={busy}>
            {busy ? "Signing in" : "Sign in"}
          </Button>
        </form>

        <div className="row-between" style={{ marginTop: "var(--s4)" }}>
          <Link to="/forgot-password" className="small">
            Forgot your password?
          </Link>
        </div>

        <p className="small muted" style={{ marginTop: "var(--s6)" }}>
          New to KaziWise? Ask your administrator for an invitation, or{" "}
          <Link to="/register">create your organisation</Link>.
        </p>
      </div>

      <aside className="auth-aside" aria-hidden="true">
        <div className="auth-aside-inner">
          <h2>Create. Assign. Learn. Assess. Prove.</h2>
          <p>
            KaziWise gives managers one place to run compliance training: build a course once,
            assign it to the right people, then watch completion and pass rates in real time.
          </p>
          <ul className="auth-points">
            <li>
              <Icon name="check" size={16} /> Upload PDFs and slide decks, chapter by chapter
            </li>
            <li>
              <Icon name="check" size={16} /> Four question types, with automatic scoring
            </li>
            <li>
              <Icon name="check" size={16} /> Shareable certificates with a verification code
            </li>
          </ul>
        </div>
      </aside>
    </div>
  );
}

export function ForgotPassword() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      // The endpoint always answers the same way, so this screen cannot be
      // used to discover which addresses are registered.
      await api.post("/v1/auth/forgot-password", { email: email.trim() });
      setSent(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Cannot reach the KaziWise server.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth">
      <div className="auth-panel">
        <div className="brand" style={{ marginBottom: "var(--s6)" }}>
          <div className="brand-mark">KW</div>
          <div>
            <div className="brand-name">KaziWise</div>
            <div className="brand-tag">Train. Track. Improve.</div>
          </div>
        </div>

        <h1>Reset your password</h1>
        <p className="soft" style={{ marginTop: 6, marginBottom: "var(--s5)" }}>
          Enter your work email and we will send a reset link.
        </p>

        {error && <Alert tone="error">{error}</Alert>}

        {sent ? (
          <Alert tone="success" title="Check your inbox">
            If that address has a KaziWise account, a reset link is on its way. The link expires in
            one hour.
          </Alert>
        ) : (
          <form
            onSubmit={submit}
            onChange={(e) => {
              if (busy) {
                e.preventDefault();
                return;
              }
            }}
          >
            <TextField
              label="Work email"
              type="email"
              value={email}
              onChange={setEmail}
              autoComplete="username"
              required
              autoFocus
            />
            <Button type="submit" size="lg" block loading={busy} disabled={busy}>
              {busy ? "Sending" : "Send reset link"}
            </Button>
          </form>
        )}

        <p style={{ marginTop: "var(--s5)" }}>
          <Link to="/login" className="small">
            Back to sign in
          </Link>
        </p>
      </div>
    </div>
  );
}

export function ChangePassword() {
  const nav = useNavigate();
  const { user, refreshProfile } = useAuth();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setFields({});

    if (next !== confirm) {
      setFields({ confirm: "The two passwords do not match." });
      return;
    }

    setBusy(true);
    try {
      await api.post("/v1/change-password", {
        current_password: current,
        new_password: next,
      });
      setDone(true);
      await refreshProfile();
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
      <div className="auth-panel">
        <h1>Change your password</h1>
        <p className="soft" style={{ marginTop: 6, marginBottom: "var(--s5)" }}>
          Signed in as {user?.email}.
        </p>

        {error && <Alert tone="error">{error}</Alert>}

        {done ? (
          <>
            <Alert tone="success" title="Password updated">
              You can now use the new password next time you sign in.
            </Alert>
            <Button block size="lg" onClick={() => nav("/", { replace: true })}>
              Continue
            </Button>
          </>
        ) : (
          <form onSubmit={submit} noValidate>
            <TextField
              label="Current password"
              type="password"
              value={current}
              onChange={setCurrent}
              autoComplete="current-password"
              error={fields.current_password}
              required
              autoFocus
            />
            <TextField
              label="New password"
              type="password"
              value={next}
              onChange={setNext}
              autoComplete="new-password"
              error={fields.new_password}
              hint="At least 10 characters, with a letter and a number."
              required
            />
            <TextField
              label="Confirm new password"
              type="password"
              value={confirm}
              onChange={setConfirm}
              autoComplete="new-password"
              error={fields.confirm}
              required
            />
            <Button type="submit" size="lg" block loading={busy} disabled={busy}>
              {busy ? <Spinner size={16} /> : null} Update password
            </Button>
          </form>
        )}
      </div>
    </div>
  );
}

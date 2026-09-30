import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { useApi } from "../lib/hooks";
import { useAuth } from "../lib/auth";
import { canViewReports, formatDate, formatPct } from "../lib/format";
import type { Certificate } from "../lib/types";
import { PageHeader } from "../components/Layout";
import {
  Alert,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Icon,
  Skeleton,
} from "../components/ui";

/** 07 - Certificate list, for admins and managers. */
export function Certificates() {
  const { user } = useAuth();
  const mayView = canViewReports(user?.role);
  const [q, setQ] = useState("");

  const { data, error, loading, initial, reload } = useApi<Certificate[]>(
    (signal) => api.get<Certificate[]>("/v1/certificates", { signal }),
  );

  if (!mayView) return <LearnerCertificates />;

  const all = data ?? [];
  const shown = q
    ? all.filter(
        (c) =>
          c.learner_name.toLowerCase().includes(q.toLowerCase()) ||
          c.course_title.toLowerCase().includes(q.toLowerCase()) ||
          c.certificate_number.toLowerCase().includes(q.toLowerCase()),
      )
    : all;

  return (
    <>
      <PageHeader
        title="Certificates"
        subtitle="Every certificate issued, with its verification code and public link."
      />

      <div className="card" style={{ marginBottom: "var(--s4)" }}>
        <div className="card-body">
          <div className="filter-bar">
            <div className="search-box">
              <Icon name="search" size={16} />
              <input
                className="input"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="Search by learner, course or certificate number"
                aria-label="Search certificates"
              />
            </div>
          </div>
        </div>
      </div>

      {error && !initial ? (
        <ErrorState error={error} onRetry={reload} />
      ) : loading && initial ? (
        <div className="grid grid-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <div className="card" key={i}>
              <div className="card-body">
                <Skeleton height={90} />
              </div>
            </div>
          ))}
        </div>
      ) : shown.length === 0 ? (
        <div className="card">
          <EmptyState
            icon="award"
            title={q ? "No certificates match" : "No certificates yet"}
            text={
              q
                ? "Try a different name, course or certificate number."
                : "A certificate is issued automatically when a learner passes a campaign that has certificates enabled."
            }
          />
        </div>
      ) : (
        <div className="grid grid-3">
          {shown.map((c) => (
            <CertificateCard key={c.id} cert={c} />
          ))}
        </div>
      )}
    </>
  );
}

function CertificateCard({ cert }: { cert: Certificate }) {
  return (
    <article className={`card cert-card${cert.valid ? "" : " cert-card-revoked"}`}>
      <div className="cert-card-top">
        <div>
          <div className="cell-strong">{cert.learner_name}</div>
          <div className="small soft">{cert.course_title}</div>
        </div>
        {cert.valid ? <Badge tone="success">Valid</Badge> : <Badge tone="danger">Revoked</Badge>}
      </div>

      <div className="cert-card-body">
        <div>
          <div className="card-sub">Score</div>
          <div style={{ fontWeight: 650, color: "var(--nav)" }}>{formatPct(cert.score)}</div>
        </div>
        <div>
          <div className="card-sub">Completed</div>
          <div style={{ fontWeight: 650, color: "var(--nav)" }}>
            {formatDate(cert.completed_at)}
          </div>
        </div>
        <div>
          <div className="card-sub">Verification code</div>
          <div className="mono-code">{cert.verification_code}</div>
        </div>
      </div>

      <div className="cert-card-foot">
        <Button
          size="sm"
          variant="secondary"
          onClick={() => window.open(cert.render_url ?? `/certificates/${cert.id}`, "_blank")}
        >
          View certificate
        </Button>
        {cert.share_url && (
          <Button
            size="sm"
            variant="ghost"
            icon={<Icon name="download" size={14} />}
            onClick={async () => {
              await navigator.clipboard.writeText(cert.share_url!);
            }}
          >
            Copy link
          </Button>
        )}
      </div>
    </article>
  );
}

/** Certificate detail. Uses the server's HTML render so there is one
 *  certificate design, and the HTML can be printed to PDF. */
export function CertificateView() {
  const { id = "" } = useParams();
  const { data, error, loading, reload } = useApi<Certificate>(
    (signal) => api.get<Certificate>(`/v1/certificates/${id}`, { signal }),
    [id],
  );

  if (loading) {
    return (
      <div className="card">
        <div className="card-body">
          <Skeleton height={260} />
        </div>
      </div>
    );
  }
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (!data) return null;

  return (
    <div className="stack">
      <div className="row-between">
        <Link to="/certificates" className="link-back">
          <Icon name="chevronLeft" size={14} /> All certificates
        </Link>
        <div className="row">
          <Button
            variant="secondary"
            onClick={() => window.open(`/v1/certificates/${data.id}/html`, "_blank")}
          >
            Open HTML
          </Button>
          <Button onClick={() => window.print()}>Print / Save PDF</Button>
        </div>
      </div>

      {!data.valid && (
        <Alert tone="error" title="This certificate has been revoked">
          {data.revoked_reason || "It is no longer valid."}
        </Alert>
      )}

      <div className="card">
        <div className="card-body">
          <CertificateBody cert={data} />
        </div>
      </div>
    </div>
  );
}

/** The certificate itself, rendered in React for the signed-in views. */
export function CertificateBody({ cert }: { cert: Certificate }) {
  return (
    <div className="certificate">
      <div className="certificate-frame">
        <div className="certificate-org">{cert.org_name || "KaziWise"}</div>
        <div className="certificate-kicker">Certificate of Completion</div>
        <div className="certificate-name">{cert.learner_name}</div>
        <div className="certificate-course">{cert.course_title}</div>

        <div className="certificate-meta">
          <div>
            <div className="certificate-meta-label">Score</div>
            <div className="certificate-meta-value">{formatPct(cert.score)}</div>
          </div>
          <div>
            <div className="certificate-meta-label">Completed</div>
            <div className="certificate-meta-value">{formatDate(cert.completed_at)}</div>
          </div>
          <div>
            <div className="certificate-meta-label">Issued</div>
            <div className="certificate-meta-value">{formatDate(cert.issued_at)}</div>
          </div>
        </div>

        <div className="certificate-foot">
          <div>
            <div className="certificate-meta-label">Certificate number</div>
            <div className="mono-code">{cert.certificate_number}</div>
          </div>
          <div>
            <div className="certificate-meta-label">Verification code</div>
            <div className="mono-code">{cert.verification_code}</div>
          </div>
        </div>

        {cert.share_url && (
          <div className="certificate-verify">
            Verify at {cert.share_url}
          </div>
        )}
      </div>
    </div>
  );
}

/** The learner's own certificates. */
export function LearnerCertificates() {
  const nav = useNavigate();
  const { data, error, loading, initial, reload } = useApi<Certificate[]>(
    (signal) => api.get<Certificate[]>("/v1/me/certificates", { signal }),
  );

  return (
    <>
      <PageHeader title="My Certificates" subtitle="Every certificate you have earned." />

      {error && !initial ? (
        <ErrorState error={error} onRetry={reload} />
      ) : loading && initial ? (
        <div className="grid grid-2">
          {Array.from({ length: 2 }).map((_, i) => (
            <div className="card" key={i}>
              <div className="card-body">
                <Skeleton height={80} />
              </div>
            </div>
          ))}
        </div>
      ) : data && data.length ? (
        <div className="grid grid-2">
          {data.map((c) => (
            <CertificateCard key={c.id} cert={c} />
          ))}
        </div>
      ) : (
        <div className="card">
          <EmptyState
            icon="award"
            title="No certificates yet"
            text="Finish your assigned training and pass the assessment to earn one."
            action={
              <Button size="sm" onClick={() => nav("/learn/training")}>
                Go to my training
              </Button>
            }
          />
        </div>
      )}
    </>
  );
}

/**
 * Public verification page. Unauthenticated: the verification code in the
 * URL is the credential. A future version adds a QR code pointing here.
 */
export function PublicCertificate() {
  const { code = "" } = useParams();
  const { data, error, loading, reload } = useApi<Certificate>(
    // The bare route returns HTML for browsers; the .json suffix returns the
    // machine-readable form this page renders.
    (signal) =>
      api.get<Certificate>(`/v1/certificates/public/${encodeURIComponent(code)}.json`, { signal }),
    [code],
  );

  if (loading) {
    return (
      <div className="public-cert">
        <div className="card" style={{ maxWidth: 760, margin: "0 auto" }}>
          <div className="card-body">
            <Skeleton height={280} />
          </div>
        </div>
      </div>
    );
  }

  if (error || !data) {
    return (
      <div className="public-cert">
        <div className="card" style={{ maxWidth: 560, margin: "0 auto" }}>
          <EmptyState
            icon="alert"
            title="Certificate not found"
            text={
              error?.message ??
              "We could not find a certificate with that verification code. Check the link, or ask your administrator to resend it."
            }
            action={
              <Button size="sm" onClick={reload}>
                Check again
              </Button>
            }
          />
        </div>
      </div>
    );
  }

  return (
    <div className="public-cert">
      <div className="public-cert-head">
        <div className="brand">
          <div className="brand-mark">KW</div>
          <div>
            <div className="brand-name">KaziWise</div>
            <div className="brand-tag">Train. Track. Improve.</div>
          </div>
        </div>
        <Badge tone={data.valid ? "success" : "danger"}>
          {data.valid ? "Verified" : "Revoked"}
        </Badge>
      </div>

      <div className="card" style={{ maxWidth: 760, margin: "0 auto" }}>
        <div className="card-body">
          <CertificateBody cert={data} />
        </div>
      </div>

      <p className="small muted" style={{ textAlign: "center", marginTop: "var(--s4)" }}>
        Anyone with this link can see this certificate. It does not expose any other employee
        information.
      </p>
    </div>
  );
}

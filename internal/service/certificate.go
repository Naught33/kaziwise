package service

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/httpx"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// ---------------------------------------------------------------------
// Certificate issue and rendering
// ---------------------------------------------------------------------

// IssueCertificate mints a certificate directly, outside an assessment.
// The spec allows an admin to award one; the pass and completion fields
// come from the caller because there is no attempt to derive them from.
func (s *Service) IssueCertificate(ctx context.Context, orgID, actorID, learnerID, courseID uuid.UUID, score float64, completedAt *time.Time) (*domain.Certificate, error) {
	user, err := s.DB.UserByID(ctx, orgID, learnerID)
	if err != nil {
		return nil, err
	}
	course, err := s.DB.CourseByID(ctx, orgID, courseID)
	if err != nil {
		return nil, err
	}
	ac := &store.AssignmentContext{
		CourseID:    courseID,
		CourseTitle: course.Title,
		LearnerID:   learnerID,
		LearnerName: user.FullName,
	}
	_ = actorID
	at := time.Now().UTC()
	if completedAt != nil {
		at = *completedAt
	}
	return s.issueCertificateFor(ctx, orgID, ac, nil, score, at)
}

// CertificateShare builds the two URLs a certificate can be shared
// through. The public viewer is the shareable link; the render URL is the
// raw certificate document, which a client may embed in an iframe.
func (s *Service) CertificateShare(cert *domain.Certificate, baseURL string) *domain.Certificate {
	base := strings.TrimRight(baseURL, "/")
	c := *cert
	c.ShareURL = fmt.Sprintf("%s/v1/certificates/public/%s", base, url.PathEscape(cert.VerificationCode))
	c.RenderURL = fmt.Sprintf("%s/v1/certificates/%s/html", base, cert.ID)
	return &c
}

// PublicCertificate backs the public verification link. It is
// deliberately unauthenticated: the verification code is the credential,
// and a revoked certificate must still render so a viewer can see that it
// was revoked rather than appearing to have never existed.
func (s *Service) PublicCertificate(ctx context.Context, code, baseURL string) (*domain.Certificate, error) {
	cert, err := s.DB.CertificateByCode(ctx, strings.TrimSpace(code))
	if err != nil {
		if isNotFound(err) {
			return nil, httpx.NewError(http.StatusNotFound, "certificate_not_found",
				"No certificate matches that verification code.")
		}
		return nil, err
	}
	return s.CertificateShare(cert, baseURL), nil
}

// RevokeCertificate withdraws a certificate. The record is kept so the
// public link resolves to a revoked notice instead of a 404.
func (s *Service) RevokeCertificate(ctx context.Context, orgID, id uuid.UUID, reason string) (*domain.Certificate, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, ValidationError("A revocation reason is required.", map[string]any{
			"reason": "Explain why this certificate is being withdrawn.",
		})
	}
	cert, err := s.DB.RevokeCertificate(ctx, orgID, id, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// ---------------------------------------------------------------------
// HTML rendering
// ---------------------------------------------------------------------

// certView is the template's data. It flattens the certificate and keeps
// presentation concerns (formatted dates, the notice for a revoked
// certificate) out of the handler.
type certView struct {
	OrgName       string
	LearnerName   string
	CourseTitle   string
	Score         string
	Number        string
	VerifyURL     string
	VerifyCode    string
	IssuedOn      string
	CompletedOn   string
	Valid         bool
	RevokedOn     string
	RevokedReason string
	OrgInitials   string
}

const certificateTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Certificate {{.Number}} &middot; {{.OrgName}}</title>
<meta name="description" content="Certificate of completion for {{.LearnerName}}.">
<meta property="og:title" content="Certificate of completion &middot; {{.LearnerName}}">
<meta property="og:description" content="{{.OrgName}} confirms completion of {{.CourseTitle}}.">
<style>
  :root {
    --ink: #10233b; --muted: #5b6b80; --line: #d8dee8;
    --gold: #b8860b; --bad: #a4272b; --paper: #ffffff; --wash: #f4f6fa;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 32px 16px; background: var(--wash); color: var(--ink);
    font-family: "Georgia", "Times New Roman", serif;
    display: flex; justify-content: center;
  }
  .sheet {
    background: var(--paper); max-width: 900px; width: 100%;
    border: 1px solid var(--line); border-radius: 4px; padding: 56px 64px;
    text-align: center;
  }
  .mark { display: flex; align-items: center; justify-content: center; gap: 12px; }
  .initials {
    width: 44px; height: 44px; border-radius: 50%; background: var(--ink);
    color: #fff; display: grid; place-items: center;
    font-family: system-ui, sans-serif; font-size: 15px; letter-spacing: .5px;
  }
  .org { font-family: system-ui, sans-serif; font-size: 15px; letter-spacing: 2.5px; text-transform: uppercase; color: var(--muted); }
  h1 { font-size: 40px; margin: 40px 0 6px; letter-spacing: 1px; }
  .sub { color: var(--muted); font-family: system-ui, sans-serif; font-size: 14px; letter-spacing: 3px; text-transform: uppercase; }
  .learner { font-size: 34px; margin: 34px 0 6px; font-style: italic; }
  .body { color: var(--muted); font-size: 17px; line-height: 1.7; margin: 18px 0 0; font-family: system-ui, sans-serif; }
  .course { font-size: 26px; margin: 10px 0 0; color: var(--ink); }
  .score { color: var(--muted); font-family: system-ui, sans-serif; font-size: 14px; margin-top: 10px; }
  .seal {
    margin: 38px auto 0; width: 92px; height: 92px; border-radius: 50%;
    border: 2px solid var(--gold); display: grid; place-items: center; color: var(--gold);
    font-family: system-ui, sans-serif; font-size: 11px; letter-spacing: 1px; text-transform: uppercase;
  }
  .rule { height: 1px; background: var(--line); margin: 40px 0 20px; }
  .meta { display: flex; justify-content: space-between; gap: 16px; font-family: system-ui, sans-serif; font-size: 13px; color: var(--muted); }
  .meta b { display: block; color: var(--ink); font-size: 14px; margin-top: 4px; }
  .verify { margin-top: 26px; font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 18px; letter-spacing: 3px; }
  .notice { border: 1px solid var(--bad); color: var(--bad); border-radius: 4px; padding: 14px 18px; margin: 26px 0 0; font-family: system-ui, sans-serif; font-size: 14px; }
  footer { margin-top: 22px; font-family: system-ui, sans-serif; font-size: 12px; color: var(--muted); }
  a { color: var(--ink); }
  @media print { body { background: #fff; padding: 0; } .sheet { border: 0; } }
</style>
</head>
<body>
<main class="sheet">
  <div class="mark">
    <div class="initials">{{.OrgInitials}}</div>
    <div class="org">{{.OrgName}}</div>
  </div>

  <h1>Certificate of Completion</h1>
  <div class="sub">KaziWise Learning &amp; Development</div>

  <p class="learner">{{.LearnerName}}</p>
  <p class="body">has successfully completed all required learning and assessment for</p>
  <p class="course">{{.CourseTitle}}</p>
  <p class="score">Final score {{.Score}}</p>

  <div class="seal">Verified</div>

  {{if not .Valid}}
  <p class="notice">
    <b>This certificate has been revoked.</b><br>
    Revoked on {{.RevokedOn}}. Reason: {{.RevokedReason}}<br>
    It is retained here only as a record and no longer confers completion.
  </p>
  {{end}}

  <div class="rule"></div>
  <div class="meta">
    <div>Certificate number<b>{{.Number}}</b></div>
    <div>Issued on<b>{{.IssuedOn}}</b></div>
    <div>Completed on<b>{{.CompletedOn}}</b></div>
  </div>

  <div class="verify">{{.VerifyCode}}</div>
  <footer>
    Verify at <a href="{{.VerifyURL}}">{{.VerifyURL}}</a>
  </footer>
</main>
</body>
</html>
`

// certTmpl is parsed once at init; a malformed template is a programming
// error and should not be rediscovered on the first certificate request.
var certTmpl = template.Must(template.New("certificate").Parse(certificateTemplate))

// RenderCertificateHTML produces the standalone certificate document.
// The input is escaped by html/template, so a learner or course name
// containing markup cannot inject into the page.
func (s *Service) RenderCertificateHTML(cert *domain.Certificate, baseURL string) ([]byte, error) {
	view := certView{
		OrgName:     cert.OrgName,
		LearnerName: cert.LearnerName,
		CourseTitle: cert.CourseTitle,
		Score:       fmt.Sprintf("%.2f%%", cert.Score),
		Number:      cert.CertificateNumber,
		VerifyCode:  cert.VerificationCode,
		IssuedOn:    cert.IssuedAt.Format("2 January 2006"),
		CompletedOn: cert.CompletedAt.Format("2 January 2006"),
		Valid:       cert.Valid,
		OrgInitials: initials(cert.OrgName),
	}
	shared := s.CertificateShare(cert, baseURL)
	view.VerifyURL = shared.ShareURL
	if !cert.Valid {
		view.RevokedOn = "—"
		if cert.RevokedAt != nil {
			view.RevokedOn = cert.RevokedAt.Format("2 January 2006")
		}
		view.RevokedReason = "unspecified"
		if cert.RevokedReason != nil {
			view.RevokedReason = *cert.RevokedReason
		}
	}
	if view.OrgName == "" {
		view.OrgName = "KaziWise"
		view.OrgInitials = "KW"
	}

	var buf strings.Builder
	if err := certTmpl.Execute(&buf, view); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// initials builds the monogram shown in the certificate seal. It uses the
// first two meaningful words and skips a leading article, so
// "Northwind Traders Ltd" reads NT rather than NL from the legal suffix.
func initials(name string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return "KW"
	}
	if len(words) == 1 {
		return headLetters(words[0], 2)
	}
	if articles[lower(words[0])] {
		words = words[1:]
	}
	if len(words) == 1 {
		return headLetters(words[0], 2)
	}
	return strings.ToUpper(letter(words[0]) + letter(words[1]))
}

// articles are leading words that carry no identity.
var articles = map[string]bool{"the": true, "a": true, "an": true}

func lower(s string) string { return strings.ToLower(s) }

// letter returns the first alphabetic character of s, or "".
func letter(s string) string {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return string(r)
		}
	}
	return ""
}

// headLetters returns the first n alphabetic characters of s, upper-cased.
// Punctuation and digits are skipped so "7-Eleven" yields "EL".
func headLetters(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
			if b.Len() >= n {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "KW"
	}
	return strings.ToUpper(b.String())
}

package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

func testCert() *domain.Certificate {
	issued := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	completed := time.Date(2026, 3, 13, 16, 5, 0, 0, time.UTC)
	return &domain.Certificate{
		ID:                uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		OrgID:             uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		LearnerID:         uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		CourseID:          uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		CertificateNumber: "KZW-2026-000042",
		VerificationCode:  "ABCD-EFGH-JKMN",
		LearnerName:       "Amina Yusuf",
		CourseTitle:       "Workplace Safety Essentials",
		Score:             87.5,
		CompletedAt:       completed,
		IssuedAt:          issued,
		OrgName:           "Acme Manufacturing",
		Valid:             true,
	}
}

func TestRenderCertificateHTML(t *testing.T) {
	s := &Service{}
	cert := testCert()

	out, err := s.RenderCertificateHTML(cert, "https://lms.example.com/")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)

	for _, want := range []string{
		"Amina Yusuf",
		"Workplace Safety Essentials",
		"KZW-2026-000042",
		"ABCD-EFGH-JKMN",
		"87.50%",
		"Acme Manufacturing",
		"14 March 2026",
		"13 March 2026",
		"Certificate of Completion",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("certificate is missing %q", want)
		}
	}
	if strings.Contains(page, "has been revoked") {
		t.Error("a valid certificate must not show the revocation notice")
	}

	// The verification link must point at the public route with a
	// trailing-slash-free base URL.
	if !strings.Contains(page, `https://lms.example.com/v1/certificates/public/ABCD-EFGH-JKMN`) {
		t.Errorf("verification link missing or malformed:\n%s", page)
	}
}

// TestRenderCertificateEscapesInput is the security check: a learner or
// course name is operator-supplied text and must never be able to inject
// script into a page that gets shared publicly.
func TestRenderCertificateEscapesInput(t *testing.T) {
	s := &Service{}
	cert := testCert()
	cert.LearnerName = `<script>alert('x')</script>`
	cert.CourseTitle = `Safety "on" <b>site</b>`

	out, err := s.RenderCertificateHTML(cert, "https://lms.example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)

	if strings.Contains(page, "<script>") {
		t.Error("learner name was not escaped: raw <script> reached the page")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("expected the script tag to appear HTML-escaped")
	}
	if strings.Contains(page, "<b>site</b>") {
		t.Error("course title markup was not escaped")
	}
}

func TestRenderRevokedCertificate(t *testing.T) {
	s := &Service{}
	cert := testCert()
	revoked := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	reason := "Issued in error"
	cert.RevokedAt = &revoked
	cert.RevokedReason = &reason
	cert.Valid = false

	out, err := s.RenderCertificateHTML(cert, "https://lms.example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)

	if !strings.Contains(page, "This certificate has been revoked") {
		t.Error("a revoked certificate must show the revocation notice")
	}
	if !strings.Contains(page, "Issued in error") {
		t.Error("the revocation reason must be shown")
	}
	if !strings.Contains(page, "1 April 2026") {
		t.Error("the revocation date must be shown")
	}
	// The record must still identify the certificate so a viewer is not
	// told it never existed.
	if !strings.Contains(page, cert.CertificateNumber) {
		t.Error("a revoked certificate must still show its number")
	}
}

func TestCertificateShareURLs(t *testing.T) {
	s := &Service{}
	cert := testCert()
	shared := s.CertificateShare(cert, "https://lms.example.com/")

	if got, want := shared.ShareURL,
		"https://lms.example.com/v1/certificates/public/ABCD-EFGH-JKMN"; got != want {
		t.Errorf("ShareURL = %q, want %q", got, want)
	}
	if got, want := shared.RenderURL,
		"https://lms.example.com/v1/certificates/"+cert.ID.String()+"/html"; got != want {
		t.Errorf("RenderURL = %q, want %q", got, want)
	}
	// The receiver must not be mutated by decorating it with URLs.
	if cert.ShareURL != "" || cert.RenderURL != "" {
		t.Error("CertificateShare mutated its input")
	}
}

func TestInitials(t *testing.T) {
	cases := map[string]string{
		"Acme Manufacturing":    "AM",
		"Northwind":             "NO",
		"Northwind Traders Ltd": "NT",
		"The Acme Group":        "AG",
		"7-Eleven":              "EL",
		"":                      "KW",
	}
	for in, want := range cases {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVerificationCodeShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code := newVerificationCode()
		if len(code) != 14 { // 3 groups of 4 plus 2 dashes
			t.Fatalf("code %q has length %d, want 14", code, len(code))
		}
		if strings.Count(code, "-") != 2 {
			t.Fatalf("code %q must contain exactly 2 dashes", code)
		}
		for _, r := range code {
			if r == '-' {
				continue
			}
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q contains %q, which is not in the unambiguous alphabet", code, r)
			}
		}
		seen[code] = true
	}
	// Collisions are rare but must not be constant.
	if len(seen) < 190 {
		t.Errorf("only %d/200 codes were distinct; generation looks degenerate", len(seen))
	}
}

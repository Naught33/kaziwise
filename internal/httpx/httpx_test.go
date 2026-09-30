package httpx

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFailLogsUnexpectedErrors is the reason a 500 is diagnosable: the body
// is deliberately generic, so the cause has to reach the log.
func TestFailLogsUnexpectedErrors(t *testing.T) {
	var logged []error
	SetInternalErrorLogger(func(err error) { logged = append(logged, err) })
	t.Cleanup(func() { SetInternalErrorLogger(nil) })

	boom := errors.New("relation \"assignments\" does not exist")
	w := httptest.NewRecorder()
	Fail(w, boom)

	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if len(logged) != 1 || !errors.Is(logged[0], boom) {
		t.Fatalf("logged = %v, want the cause", logged)
	}
	// The client must not learn anything about the internals.
	if body := w.Body.String(); strings.Contains(body, "does not exist") {
		t.Errorf("body leaks the cause: %s", body)
	}
}

// TestFailLogsWrappedInternalErrors covers the other route to a 500: an
// application error that already carries a cause.
func TestFailLogsWrappedInternalErrors(t *testing.T) {
	var logged []error
	SetInternalErrorLogger(func(err error) { logged = append(logged, err) })
	t.Cleanup(func() { SetInternalErrorLogger(nil) })

	boom := errors.New("connection reset")
	w := httptest.NewRecorder()
	Fail(w, Internal(boom))

	if len(logged) != 1 || !errors.Is(logged[0], boom) {
		t.Fatalf("logged = %v, want the cause", logged)
	}
}

// TestFailDoesNotLogClientErrors keeps expected 4xx outcomes out of the error
// log: a 404 or a validation failure is not an incident.
func TestFailDoesNotLogClientErrors(t *testing.T) {
	var logged []error
	SetInternalErrorLogger(func(err error) { logged = append(logged, err) })
	t.Cleanup(func() { SetInternalErrorLogger(nil) })

	for _, err := range []error{
		NotFound("Course"),
		BadRequest("invalid_json", "Body is not valid JSON."),
		FieldError("email", "Enter a valid email address."),
		Forbidden("Your role does not allow this action."),
	} {
		Fail(httptest.NewRecorder(), err)
	}
	if len(logged) != 0 {
		t.Errorf("logged = %v, want nothing for client errors", logged)
	}
}

// TestFailReportsSlogError checks the hook is usable as a plain slog sink.
func TestFailReportsSlogError(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	SetInternalErrorLogger(func(err error) { log.Error("unhandled error", "error", err) })
	t.Cleanup(func() { SetInternalErrorLogger(nil) })

	Fail(httptest.NewRecorder(), errors.New("ambiguous column name"))
	out := buf.String()
	if !strings.Contains(out, "unhandled error") || !strings.Contains(out, "ambiguous column name") {
		t.Errorf("log = %q, want the error text", out)
	}
}

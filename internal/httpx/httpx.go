// Package httpx provides the shared HTTP conventions for the API:
// one error envelope, one success envelope, decoding helpers and
// pagination, so every handler behaves identically for the front end.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Error is an application error carrying an HTTP status and a stable
// machine-readable code. Handlers return *Error; the router renders it.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithFields attaches per-field validation detail.
func (e *Error) WithFields(f map[string]any) *Error { e.Fields = f; return e }

// WithCause wraps the underlying error for logs (never serialised).
func (e *Error) WithCause(err error) *Error { e.cause = err; return e }

func NewError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func BadRequest(code, msg string) *Error { return NewError(http.StatusBadRequest, code, msg) }
func Unauthorized(msg string) *Error     { return NewError(http.StatusUnauthorized, "unauthorized", msg) }
func Forbidden(msg string) *Error        { return NewError(http.StatusForbidden, "forbidden", msg) }
func NotFound(what string) *Error {
	return NewError(http.StatusNotFound, "not_found", what+" not found")
}
func Conflict(code, msg string) *Error { return NewError(http.StatusConflict, code, msg) }
func Unprocessable(msg string) *Error {
	return NewError(http.StatusUnprocessableEntity, "validation_failed", msg)
}
func TooLarge(msg string) *Error {
	return NewError(http.StatusRequestEntityTooLarge, "payload_too_large", msg)
}
func Internal(cause error) *Error {
	e := NewError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred. Please retry.")
	return e.WithCause(cause)
}

// Frequently used validation errors.
func Validation(msg string) *Error { return Unprocessable(msg) }

func FieldError(field, msg string) *Error {
	return Unprocessable(msg).WithFields(map[string]any{field: msg})
}

// IsNotFound reports whether err is (or wraps) a 404 application error.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// ---------------------------------------------------------------------
// Success envelope
// ---------------------------------------------------------------------

// Body is the canonical success payload shape.
type Body struct {
	Data  any        `json:"data"`
	Meta  *Meta      `json:"meta,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
}

// Meta carries pagination and other collection metadata.
type Meta struct {
	Page       int    `json:"page,omitempty"`
	PerPage    int    `json:"per_page,omitempty"`
	Total      int    `json:"total,omitempty"`
	TotalPages int    `json:"total_pages,omitempty"`
	HasNext    bool   `json:"has_next,omitempty"`
	HasPrev    bool   `json:"has_prev,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Filters    any    `json:"filters,omitempty"`
}

// ErrorBody is the serialised form of *Error (the cause is stripped).
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// JSON writes 200 with {"data": ...}.
func JSON(w http.ResponseWriter, data any) { JSONStatus(w, http.StatusOK, data, nil) }

// JSONMeta writes 200 with {"data": ..., "meta": ...}.
func JSONMeta(w http.ResponseWriter, data any, meta *Meta) {
	JSONStatus(w, http.StatusOK, data, meta)
}

func Created(w http.ResponseWriter, data any) { JSONStatus(w, http.StatusCreated, data, nil) }

func Accepted(w http.ResponseWriter, data any) { JSONStatus(w, http.StatusAccepted, data, nil) }

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

func JSONStatus(w http.ResponseWriter, status int, data any, meta *Meta) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data == nil && meta == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(Body{Data: data, Meta: meta})
}

// internalErrorLogger receives the cause of any error that is not already an
// application error. Those become a 500 whose body is deliberately generic,
// so without this the server logs a status code and nothing about the cause.
var internalErrorLogger func(error)

// SetInternalErrorLogger installs the sink for unexpected errors. The server
// sets it once at construction; a nil function disables the hook.
func SetInternalErrorLogger(fn func(error)) { internalErrorLogger = fn }

// Fail renders any error as the standard error envelope.
func Fail(w http.ResponseWriter, err error) {
	var appErr *Error
	if !errors.As(err, &appErr) {
		reportInternal(err)
		appErr = Internal(err)
	} else if appErr.Status >= 500 {
		cause := appErr.Unwrap()
		if cause == nil {
			cause = appErr
		}
		reportInternal(cause)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(appErr.Status)
	_ = json.NewEncoder(w).Encode(Body{
		Error: &ErrorBody{Code: appErr.Code, Message: appErr.Message, Fields: appErr.Fields},
	})
}

func reportInternal(err error) {
	if internalErrorLogger != nil && err != nil {
		internalErrorLogger(err)
	}
}

// ---------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------

const maxBodyBytes = 8 << 20 // 8 MiB of JSON; binaries use multipart.

// Decode reads and validates a JSON request body. Unknown fields are
// rejected so typos in the front end surface immediately.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return BadRequest("missing_body", "A JSON request body is required.")
	}
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return NewError(http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Content-Type must be application/json.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return TooLarge("Request body exceeds 8 MiB.")
		case errors.Is(err, io.EOF):
			return BadRequest("missing_body", "A JSON request body is required.")
		default:
			return BadRequest("invalid_json", "Request body is not valid JSON: "+err.Error())
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Query parameters
// ---------------------------------------------------------------------

// Page is the parsed pagination + filter bag for list endpoints.
type Page struct {
	Page        int
	PerPage     int
	Offset      int
	Search      string
	Sort        string
	Order       string
	Status      string
	Overdue     *bool
	NeedsReview *bool
	RawQuery    map[string]string
}

const (
	DefaultPerPage = 20
	MaxPerPage     = 200
)

// Query parses pagination, search and sort parameters.
func Query(r *http.Request) Page {
	q := r.URL.Query()
	p := Page{
		Page:     atoiDefault(q.Get("page"), 1),
		PerPage:  clamp(atoiDefault(q.Get("per_page"), DefaultPerPage), 1, MaxPerPage),
		Search:   strings.TrimSpace(q.Get("search")),
		Sort:     strings.TrimSpace(q.Get("sort")),
		Order:    strings.ToLower(strings.TrimSpace(q.Get("order"))),
		Status:   strings.TrimSpace(q.Get("status")),
		RawQuery: map[string]string{},
	}
	if p.Page < 1 {
		p.Page = 1
	}
	p.Offset = (p.Page - 1) * p.PerPage
	if p.Order != "asc" && p.Order != "desc" {
		p.Order = "desc"
	}
	for k, v := range q {
		if len(v) > 0 {
			p.RawQuery[k] = v[0]
		}
	}
	// Only the two boolean filters the list endpoints need are parsed. An
	// unrecognised value is treated as absent rather than as false, so a
	// typo cannot silently widen a result set.
	if v := strings.TrimSpace(q.Get("overdue")); v == "true" || v == "false" {
		b := v == "true"
		p.Overdue = &b
	}
	if v := strings.TrimSpace(q.Get("needs_review")); v == "true" || v == "false" {
		b := v == "true"
		p.NeedsReview = &b
	}
	return p
}

func (p Page) Meta(total int) *Meta {
	totalPages := 0
	if p.PerPage > 0 {
		totalPages = (total + p.PerPage - 1) / p.PerPage
	}
	return &Meta{
		Page:       p.Page,
		PerPage:    p.PerPage,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    p.Page < totalPages,
		HasPrev:    p.Page > 1,
		Sort:       strings.TrimSpace(p.Sort + " " + p.Order),
	}
}

// UUIDParam reads a path parameter as a UUID-shaped string (validated
// downstream by the store so we can return a clean 400 here).
func UUIDParam(r *http.Request, key string) (string, error) {
	v := chi.URLParam(r, key)
	if v == "" {
		return "", BadRequest("missing_parameter", "Path parameter '"+key+"' is required.")
	}
	return v, nil
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

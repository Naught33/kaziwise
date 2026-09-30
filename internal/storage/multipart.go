package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
)

// bodyBuilder builds a single-file multipart/form-data body in memory.
// A single part means the whole body fits comfortably in the buffer and
// the upload stays a single request/response round trip.
type bodyBuilder struct {
	buf    *bytes.Buffer
	writer *multipart.Writer
	err    error
}

func newMultipart(buf *bytes.Buffer) *bodyBuilder {
	w := multipart.NewWriter(buf)
	return &bodyBuilder{buf: buf, writer: w}
}

func (m *bodyBuilder) writeFile(field, filename, mimeType string, r io.Reader, size int64) error {
	if m.err != nil {
		return m.err
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name=%q; filename=%q`, field, escapeQuotes(filepath.Base(filename))))
	h.Set("Content-Type", mimeType)
	part, err := m.writer.CreatePart(h)
	if err != nil {
		m.err = err
		return err
	}
	// Copy through a limit so an oversized upload cannot exhaust memory
	// even if the caller mis-declared Content-Length.
	limited := io.LimitReader(r, size+1)
	n, err := io.Copy(part, limited)
	if err != nil {
		m.err = err
		return err
	}
	if size >= 0 && n > size {
		m.err = ErrTooLarge
		return ErrTooLarge
	}
	return nil
}

func (m *bodyBuilder) close() error {
	if m.err != nil {
		return m.err
	}
	return m.writer.Close()
}

// contentType exposes the generated boundary so the caller can set the
// request Content-Type header.
func (m *bodyBuilder) contentType() string { return m.writer.FormDataContentType() }

func escapeQuotes(s string) string {
	out := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// ErrTooLarge is returned when a stream carries more bytes than declared.
var ErrTooLarge = errors.New("uploaded object exceeds the declared size")

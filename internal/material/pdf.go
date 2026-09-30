package material

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// parsePDF walks the PDF page by page, extracts text, marks blank pages
// as chapter delimiters and derives chapters.
//
// The renderer is not invoked: KaziWise stores the original file and hands
// the client a signed URL plus a page number, so a browser PDF viewer
// (pdf.js) or a converted image sequence displays the page exactly as
// authored. Text extraction exists to find blank pages, to title
// chapters, and to make the material searchable.
func parsePDF(data []byte, opts Options) (*Result, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// Fall back to the password-aware constructor, which also covers
		// encrypted files that the plain reader rejects.
		r, err = pdf.NewReaderEncrypted(bytes.NewReader(data), int64(len(data)),
			func() string { return "" })
		if err != nil {
			return nil, fmt.Errorf("read pdf: %w", err)
		}
	}
	if r == nil {
		return nil, errors.New("pdf could not be opened")
	}

	n := r.NumPage()
	if n == 0 {
		return nil, errors.New("pdf contains no pages")
	}

	var warnings []string
	// An /Encrypt entry in the trailer marks a protected document.
	if !r.Trailer().Key("Encrypt").IsNull() {
		warnings = append(warnings,
			"This PDF is encrypted. Blank pages are detected from the decrypted text where possible; "+
				"if extraction is incomplete the whole file is treated as a single chapter.")
	}

	pages := make([]Page, 0, n)
	for i := 1; i <= n; i++ {
		p := r.Page(i)
		if p.V.Kind() == pdf.Null {
			warnings = append(warnings, fmt.Sprintf("page %d could not be read", i))
			continue
		}
		text, err := pageText(p)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("page %d text extraction failed: %v", i, err))
		}
		text = NormalizeText(text)
		pages = append(pages, Page{
			Number:  i,
			IsBlank: IsBlankText(text, opts.MinCharsPerPage),
			Text:    text,
		})
	}
	if len(pages) == 0 {
		return nil, errors.New("pdf contains no readable pages")
	}

	// A scanned PDF has no text layer at all. Warn rather than silently
	// declaring every page blank, which would create one chapter per page.
	allBlank := true
	for _, p := range pages {
		if !p.IsBlank {
			allBlank = false
			break
		}
	}
	if allBlank && len(pages) > 1 {
		warnings = append(warnings,
			"No text layer was found. This looks like a scanned PDF, so it is treated as a single chapter "+
				"and no blank pages are treated as chapter breaks.")
		pages = collapseAll(pages)
	}

	chapters := DeriveChapters(pages, opts)
	return &Result{
		Kind:      KindPDF,
		PageCount: len(pages),
		Pages:     pages,
		Chapters:  chapters,
		Warnings:  warnings,
	}, nil
}

// pageText pulls a page's text out, degrading to the styled-text path if
// the plain path reports a font problem.
func pageText(p pdf.Page) (string, error) {
	fonts := map[string]*pdf.Font{}
	for _, name := range p.Fonts() {
		f := p.Font(name)
		fonts[name] = &f
	}
	text, err := p.GetPlainText(fonts)
	if err == nil && strings.TrimSpace(text) != "" {
		return text, nil
	}

	// Retry without the font map: works for some producers that
	// mis-declare their font resources.
	if text2, err2 := p.GetPlainText(nil); err2 == nil && strings.TrimSpace(text2) != "" {
		return text2, nil
	}
	if err != nil {
		return "", err
	}
	return text, nil
}

// collapseAll makes every page renderable inside one chapter, used when
// text extraction is impossible.
func collapseAll(pages []Page) []Page {
	out := make([]Page, 0, len(pages))
	for i, p := range pages {
		p.IsBlank = false
		p.ChapterIndex = 1
		p.Number = i + 1
		out = append(out, p)
	}
	return out
}

// PDFPageTexts is a convenience for tests and the seeding tool: it returns
// the raw text of every page without chapter derivation.
func PDFPageTexts(data []byte) ([]string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, r.NumPage())
	for i := 1; i <= r.NumPage(); i++ {
		t, _ := pageText(r.Page(i))
		out = append(out, strings.TrimSpace(t))
	}
	return out, nil
}

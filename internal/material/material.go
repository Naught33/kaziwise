// Package material parses uploaded course material (PDF and PPTX) into
// individual pages/slides and derives chapters from blank pages.
//
// The rule KaziWise uses, as specified: a BLANK page or slide is a
// chapter delimiter. A blank page is stored (it is part of the source
// document and is needed to reproduce the document faithfully) but it is
// never rendered to a learner. Rendering skips over it and opens the next
// chapter.
//
//	page 1  "Chapter 1 ..."   -> chapter 1, rendered
//	page 2  text              -> chapter 1, rendered
//	page 3  ""  (blank)       -> DELIMITER, skipped
//	page 4  "Chapter 2 ..."   -> chapter 2, rendered
//	page 5  ""  (blank)       -> DELIMITER, skipped
//	page 6  "Chapter 3 ..."   -> chapter 3, rendered
package material

import (
	"path/filepath"
	"strings"
)

// Page is one parsed page or slide.
type Page struct {
	// Number is the 1-based position in the SOURCE document. It never
	// changes, so a viewer can be pointed straight at the original page.
	Number int
	// IsBlank marks a deliberate empty page: a chapter delimiter.
	IsBlank bool
	// ChapterIndex is the 1-based chapter this page belongs to, counting
	// from the first delimiter. A leading blank page is ignored so a
	// document never opens with an empty chapter.
	ChapterIndex int
	// ChapterTitle is a best-effort heading: the first non-blank line of
	// the first page of the chapter, when that line is short enough to
	// read like a title.
	ChapterTitle string
	// Text is the extracted text, used for search, accessibility and
	// server-side content review.
	Text string
}

// Chapter groups consecutive rendered pages.
type Chapter struct {
	Index      int    `json:"index"`
	Title      string `json:"title,omitempty"`
	PageStart  int    `json:"page_start"`
	PageEnd    int    `json:"page_end"`
	Renderable int    `json:"renderable_page_count"`
}

// Result is the outcome of parsing one asset.
type Result struct {
	Kind      Kind      `json:"kind"`
	PageCount int       `json:"page_count"`
	Chapters  []Chapter `json:"chapters"`
	Pages     []Page    `json:"pages"`
	// Warnings collects non-fatal issues (encrypted file, unsupported
	// image-only scan) so the admin sees them in the builder.
	Warnings []string `json:"warnings,omitempty"`
}

type Kind string

const (
	KindPDF   Kind = "pdf"
	KindPPTX  Kind = "pptx"
	KindImage Kind = "image"
	KindVideo Kind = "video"
	KindOther Kind = "other"
)

// Options tunes the parser.
type Options struct {
	// MinCharsPerPage is the text threshold below which a page is
	// considered blank. A page with a stray page number or a single
	// stray character should not open a new chapter.
	MinCharsPerPage int
	// MaxTitleLen bounds what the parser will treat as a chapter title.
	MaxTitleLen int
	// ChapterTitleFromPage is the 1-based page within a chapter that the
	// title is read from (default: the first rendered page).
	ChapterTitleFromPage int
}

func (o *Options) withDefaults() Options {
	if o.MinCharsPerPage <= 0 {
		o.MinCharsPerPage = 8
	}
	if o.MaxTitleLen <= 0 {
		o.MaxTitleLen = 80
	}
	if o.ChapterTitleFromPage <= 0 {
		o.ChapterTitleFromPage = 1
	}
	return *o
}

// DetectKind classifies an upload by extension then content type.
func DetectKind(filename, contentType string) Kind {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return KindPDF
	case ".pptx", ".ppt":
		return KindPPTX
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".avif":
		return KindImage
	case ".mp4", ".webm", ".mov", ".m4v", ".ogg", ".avi", ".mkv":
		return KindVideo
	case ".doc", ".docx", ".txt", ".rtf", ".odt":
		return KindOther
	}
	switch {
	case strings.Contains(contentType, "pdf"):
		return KindPDF
	case strings.Contains(contentType, "presentation"), strings.Contains(contentType, "powerpoint"):
		return KindPPTX
	case strings.HasPrefix(contentType, "image/"):
		return KindImage
	case strings.HasPrefix(contentType, "video/"):
		return KindVideo
	}
	return KindOther
}

// IsPaginated reports whether a kind is rendered page by page and
// therefore subject to the blank-page chapter rule.
func (k Kind) IsPaginated() bool { return k == KindPDF || k == KindPPTX }

// Parse dispatches on the asset kind. Images and videos are single page
// assets and never contain delimiters.
func Parse(kind Kind, data []byte, opts Options) (*Result, error) {
	opts = opts.withDefaults()
	switch kind {
	case KindPDF:
		return parsePDF(data, opts)
	case KindPPTX:
		return parsePPTX(data, opts)
	case KindImage, KindVideo:
		return singlePage(kind), nil
	default:
		return &Result{
			Kind:      kind,
			PageCount: 1,
			Pages:     []Page{{Number: 1, ChapterIndex: 1}},
			Warnings:  []string{"This file type is not paginated; it is attached to the lesson as a single download."},
		}, nil
	}
}

func singlePage(k Kind) *Result {
	return &Result{
		Kind:      k,
		PageCount: 1,
		Pages:     []Page{{Number: 1, ChapterIndex: 1}},
	}
}

// ---------------------------------------------------------------------
// Chapter derivation
// ---------------------------------------------------------------------

// DeriveChapters walks the pages in order, opens a new chapter on every
// blank page, and skips blank pages for rendering. It is exported so the
// same rule can be re-applied to any page list.
func DeriveChapters(pages []Page, opts Options) []Chapter {
	opts = opts.withDefaults()
	var chapters []Chapter
	cur := Chapter{Index: 1, PageStart: 0, PageEnd: 0}
	started := false

	for i := range pages {
		p := &pages[i]
		if p.IsBlank {
			// DELIMITER: close the open chapter and skip this page.
			if started {
				chapters = append(chapters, cur)
			}
			started = false
			continue
		}
		if !started {
			cur = Chapter{Index: len(chapters) + 1, PageStart: p.Number, PageEnd: p.Number}
			started = true
		}
		cur.PageEnd = p.Number
		cur.Renderable++
		p.ChapterIndex = cur.Index
		if cur.Title == "" && cur.Renderable == opts.ChapterTitleFromPage {
			cur.Title = guessTitle(p.Text, opts.MaxTitleLen)
		}
	}
	if started {
		chapters = append(chapters, cur)
	}
	// No content at all: keep one empty chapter so the UI has structure.
	if len(chapters) == 0 && len(pages) > 0 {
		chapters = append(chapters, Chapter{Index: 1, PageStart: 1, PageEnd: 1})
	}
	return chapters
}

// guessTitle returns the first line of a page when it reads like a
// heading rather than body copy.
func guessTitle(text string, maxLen int) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len([]rune(line)) > maxLen {
			return ""
		}
		return line
	}
	return ""
}

// IsBlankText applies the blankness rule to a page's extracted text.
// Whitespace-only, and text with too little readable content to count as
// a real page, both count as blank.
func IsBlankText(text string, minChars int) bool {
	return len([]rune(runeClean(text))) < minChars
}

// ---------------------------------------------------------------------
// Text hygiene
// ---------------------------------------------------------------------

// NormalizeText collapses the whitespace that PDF/PPTX extraction
// produces without destroying paragraph boundaries.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\u0000", "")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.Join(strings.Fields(ln), " ")
		out = append(out, ln)
	}
	// Collapse runs of blank lines.
	compact := make([]string, 0, len(out))
	blank := 0
	for _, ln := range out {
		if ln == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		compact = append(compact, ln)
	}
	return strings.TrimSpace(strings.Join(compact, "\n"))
}

// isZIP reports whether data starts with the ZIP local-file-header magic
// number. Used to sanity-check uploads whose extension may lie.
func isZIP(data []byte) bool {
	return len(data) > 2 && data[0] == 'P' && data[1] == 'K'
}

// SniffKind refines DetectKind using the file's magic bytes when the
// extension is missing or wrong.
func SniffKind(filename, contentType string, data []byte) Kind {
	kind := DetectKind(filename, contentType)
	if len(data) < 4 {
		return kind
	}
	if string(data[:4]) == "%PDF" {
		return KindPDF
	}
	if isZIP(data) {
		// A .docx is also a zip; only presentation zips are slides.
		if kind == KindPPTX || strings.Contains(contentType, "presentation") {
			return KindPPTX
		}
	}
	return kind
}

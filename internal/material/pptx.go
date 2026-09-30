package material

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// parsePPTX reads an Office Open XML presentation. A slide is blank when
// it carries no text AND no embedded picture, because a slide whose only
// content is a full-bleed image is a content page, not a delimiter.
func parsePPTX(data []byte, opts Options) (*Result, error) {
	zr, err := zip.NewReader(NewBytesReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open pptx archive: %w", err)
	}

	slides := map[int]*zip.File{}
	for _, f := range zr.File {
		name := f.Name
		if !strings.HasPrefix(name, "ppt/slides/slide") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		base := strings.TrimSuffix(strings.TrimPrefix(name, "ppt/slides/slide"), ".xml")
		n, err := strconv.Atoi(base)
		if err != nil {
			continue
		}
		slides[n] = f
	}
	if len(slides) == 0 {
		return nil, errors.New("pptx contains no slides")
	}

	nums := make([]int, 0, len(slides))
	for n := range slides {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	// Slide numbering is contiguous 1..N in a valid file, but guard
	// against sparse archives by mapping to positional page numbers.
	pageOf := make(map[int]int, len(nums))
	for i, n := range nums {
		pageOf[n] = i + 1
	}

	pages := make([]Page, 0, len(nums))
	var warnings []string
	for _, n := range nums {
		xmlBytes, err := readZipFile(slides[n])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("slide%d.xml could not be read: %v", n, err))
			continue
		}
		text, hasImage, err := slideText(xmlBytes)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("slide%d could not be parsed: %v", n, err))
		}
		blank := IsBlankText(text, opts.MinCharsPerPage) && !hasImage
		pages = append(pages, Page{
			Number:  pageOf[n],
			IsBlank: blank,
			Text:    NormalizeText(text),
		})
	}
	if len(pages) == 0 {
		return nil, errors.New("pptx contains no readable slides")
	}

	chapters := DeriveChapters(pages, opts)
	return &Result{
		Kind:      KindPPTX,
		PageCount: len(pages),
		Pages:     pages,
		Chapters:  chapters,
		Warnings:  warnings,
	}, nil
}

// slideText returns the concatenated text of a slide and whether it
// embeds a picture. A tolerant streaming pass over the XML is used rather
// than a rigid struct so text inside grouped shapes, tables, charts and
// SmartArt is captured too - all of those store their labels in <a:t>.
//
// A slide counts as blank only when it has neither text nor a picture: a
// full-bleed image slide is content, not a chapter delimiter.
func slideText(data []byte) (string, bool, error) {
	var out strings.Builder
	var paragraph strings.Builder
	inTextRun := false
	hasImage := false

	flushParagraph := func() {
		line := strings.TrimSpace(paragraph.String())
		paragraph.Reset()
		if line == "" {
			return
		}
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(line)
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Malformed but recoverable: keep everything collected so far.
			flushParagraph()
			return out.String(), hasImage, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inTextRun = true
			case "p":
				flushParagraph()
			case "blip":
				for _, a := range t.Attr {
					if (a.Name.Local == "embed" || a.Name.Local == "link") && a.Value != "" {
						hasImage = true
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inTextRun = false
			} else if t.Name.Local == "p" {
				flushParagraph()
			}
		case xml.CharData:
			if inTextRun {
				paragraph.Write(t)
			}
		}
	}
	flushParagraph()
	return out.String(), hasImage, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 64<<20))
}

// bytesReaderAt adapts a byte slice to io.ReaderAt for archive/zip.
type bytesReaderAt struct{ b []byte }

func (r *bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(r.b)) {
		return 0, io.EOF
	}
	n := copy(p, r.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// NewBytesReader adapts a byte slice to io.ReaderAt for archive/zip.
func NewBytesReader(b []byte) *bytesReaderAt { return &bytesReaderAt{b: b} }

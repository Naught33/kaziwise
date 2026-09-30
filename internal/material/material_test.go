package material

import "testing"

// The chapter rule: a blank page opens a new chapter and is never
// rendered. A leading blank page must not create an empty first chapter.
func TestDeriveChaptersSkipsBlankDelimiters(t *testing.T) {
	pages := []Page{
		{Number: 1, Text: "Chapter 1: Introduction"},
		{Number: 2, Text: "Body copy for chapter one."},
		{Number: 3, IsBlank: true},
		{Number: 4, Text: "Chapter 2: Fire Safety"},
		{Number: 5, Text: "More body copy here."},
		{Number: 6, IsBlank: true},
		{Number: 7, Text: "Chapter 3: Reporting"},
	}

	chapters := DeriveChapters(pages, Options{})

	if len(chapters) != 3 {
		t.Fatalf("want 3 chapters, got %d (%+v)", len(chapters), chapters)
	}
	if chapters[0].PageStart != 1 || chapters[0].PageEnd != 2 {
		t.Errorf("chapter 1 should span pages 1-2, got %d-%d", chapters[0].PageStart, chapters[0].PageEnd)
	}
	if chapters[1].PageStart != 4 || chapters[1].PageEnd != 5 {
		t.Errorf("chapter 2 should span pages 4-5, got %d-%d", chapters[1].PageStart, chapters[1].PageEnd)
	}
	if chapters[2].PageStart != 7 || chapters[2].PageEnd != 7 {
		t.Errorf("chapter 3 should span page 7, got %d-%d", chapters[2].PageStart, chapters[2].PageEnd)
	}
	// Blank pages must never be counted as renderable.
	total := 0
	for _, c := range chapters {
		total += c.Renderable
	}
	if total != 5 {
		t.Errorf("want 5 renderable pages, got %d", total)
	}
	// Chapter indices are stamped on the renderable pages only.
	for _, p := range pages {
		if p.IsBlank {
			if p.ChapterIndex != 0 {
				t.Errorf("blank page %d should not carry a chapter index, got %d", p.Number, p.ChapterIndex)
			}
			continue
		}
		if p.ChapterIndex == 0 {
			t.Errorf("renderable page %d has no chapter index", p.Number)
		}
	}
}

func TestDeriveChaptersLeadingBlankIsIgnored(t *testing.T) {
	pages := []Page{
		{Number: 1, IsBlank: true},
		{Number: 2, Text: "Real content starts here."},
	}
	chapters := DeriveChapters(pages, Options{})
	if len(chapters) != 1 {
		t.Fatalf("want 1 chapter, got %d", len(chapters))
	}
	if chapters[0].PageStart != 2 {
		t.Errorf("chapter should start at page 2, got %d", chapters[0].PageStart)
	}
}

func TestDeriveChaptersTrailingBlankClosesDocument(t *testing.T) {
	pages := []Page{
		{Number: 1, Text: "Only content page here."},
		{Number: 2, IsBlank: true},
	}
	chapters := DeriveChapters(pages, Options{})
	if len(chapters) != 1 {
		t.Fatalf("want 1 chapter, got %d", len(chapters))
	}
	if chapters[0].PageEnd != 1 {
		t.Errorf("chapter should end at page 1, got %d", chapters[0].PageEnd)
	}
}

func TestDeriveChaptersConsecutiveBlanks(t *testing.T) {
	pages := []Page{
		{Number: 1, Text: "Chapter one content."},
		{Number: 2, IsBlank: true},
		{Number: 3, IsBlank: true},
		{Number: 4, IsBlank: true},
		{Number: 5, Text: "Chapter two content."},
	}
	chapters := DeriveChapters(pages, Options{})
	if len(chapters) != 2 {
		t.Fatalf("want 2 chapters, got %d (%+v)", len(chapters), chapters)
	}
}

func TestIsBlankText(t *testing.T) {
	cases := []struct {
		in    string
		min   int
		blank bool
	}{
		{"", 8, true},
		{"   \n\t  ", 8, true},
		{"3", 8, true},            // a bare page number
		{"-", 8, true},            // a stray rule
		{"Fire safety", 8, false}, // real content
		{"Short", 8, true},        // below the threshold
		{"\u200b\u200b", 8, true}, // zero width only
		{"0123456789", 8, false},  // digits do count as content
		{"Full sentence of real text.", 8, false},
	}
	for _, c := range cases {
		if got := IsBlankText(c.in, c.min); got != c.blank {
			t.Errorf("IsBlankText(%q, %d) = %v, want %v", c.in, c.min, got, c.blank)
		}
	}
}

func TestDetectAndSniffKind(t *testing.T) {
	if k := DetectKind("deck.PPTX", ""); k != KindPPTX {
		t.Errorf("DetectKind(pptx) = %v", k)
	}
	if k := DetectKind("handout.pdf", ""); k != KindPDF {
		t.Errorf("DetectKind(pdf) = %v", k)
	}
	if k := DetectKind("clip.mp4", ""); k != KindVideo {
		t.Errorf("DetectKind(mp4) = %v", k)
	}
	// A mislabelled upload is corrected from its magic bytes.
	if k := SniffKind("notes.txt", "application/octet-stream", []byte("%PDF-1.7\n")); k != KindPDF {
		t.Errorf("SniffKind should detect PDF magic, got %v", k)
	}
	if k := SniffKind("deck.pptx", "", []byte("PK\x03\x04")); k != KindPPTX {
		t.Errorf("SniffKind should keep pptx, got %v", k)
	}
}

func TestNormalizeText(t *testing.T) {
	got := NormalizeText("Line  one\r\n\r\n\r\nLine   two\nLine three\x00")
	// Runs of blank lines collapse to a single blank line; single ones
	// survive so paragraph boundaries are not destroyed.
	want := "Line one\n\nLine two\nLine three"
	if got != want {
		t.Errorf("NormalizeText = %q, want %q", got, want)
	}
}

package material

import (
	"strings"
	"unicode"
)

// runeClean keeps only letters and digits. A page showing just "3" must
// still count as blank so it can act as a chapter delimiter, while
// zero-width joiners, soft hyphens and control characters must not
// defeat the rule.
func runeClean(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

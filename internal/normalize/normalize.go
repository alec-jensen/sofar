package normalize

import (
	"regexp"
	"strings"
)

var (
	nonLetters = regexp.MustCompile(`[^a-z]+`)
	spaces     = regexp.MustCompile(`\s+`)
)

// Normalize lowercases, drops ".com" and everything that is not a letter, and
// collapses whitespace, so descriptors compare as whole phrases.
func Normalize(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(nonLetters.ReplaceAllString(strings.ReplaceAll(strings.ToLower(s), ".com", ""), " "), " "))
}

// Matches reports whether two names refer to the same thing by phrase
// containment; very short names must match exactly.
func Matches(a, b string) bool {
	a, b = Normalize(a), Normalize(b)
	if len(a) < 3 || len(b) < 3 {
		return a == b && a != ""
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

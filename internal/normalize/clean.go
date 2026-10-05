package normalize

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDescriptionLen caps how much of a bank string is ever looked at. Bank
// strings are untrusted input (CLAUDE.md non-negotiable 9).
const MaxDescriptionLen = 300

// Cleaned is a bank description broken into what identifies the merchant and
// the noise around it. The processor stays as a separate feature (S18a).
type Cleaned struct {
	Name      string // readable merchant name
	Key       string // normalized key for matching and grouping
	Processor string // "square", "toast", "paypal", ... or ""
	StoreNo   string
	Raw       string // the sanitized input
}

// Sanitize strips control characters and bidirectional overrides, collapses
// whitespace and truncates. It never fails: bank strings are untrusted.
func Sanitize(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r), r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0xFEFF, r == 0x200B:
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > MaxDescriptionLen {
		s = strings.ToValidUTF8(s[:MaxDescriptionLen], "")
	}
	return s
}

var processors = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)^(sq|sqc)\s*\*\s*`), "square"},
	{regexp.MustCompile(`(?i)^tst\s*\*\s*`), "toast"},
	{regexp.MustCompile(`(?i)^(pp|paypal)\s*\*\s*`), "paypal"},
	{regexp.MustCompile(`(?i)^(py|pay)\s*\*\s*`), "paypal"},
	{regexp.MustCompile(`(?i)^sp\s*\*\s*`), "shopify"},
	{regexp.MustCompile(`(?i)^(goog|google)\s*\*\s*`), "google"},
	{regexp.MustCompile(`(?i)^(dd|doordash)\s*\*\s*`), "doordash"},
	{regexp.MustCompile(`(?i)^(in|ic|bt|clover)\s*\*\s*`), "other"},
	{regexp.MustCompile(`(?i)^master money\s+`), "mastercard"},
}

var (
	reDate    = regexp.MustCompile(`\b(0?[1-9]|1[0-2])/(0?[1-9]|[12]\d|3[01])(/(\d{2}|\d{4}))?\b`)
	rePhone   = regexp.MustCompile(`\b\(?\d{3}\)?[-. ]?\d{3}[-. ]\d{4}\b`)
	reCard    = regexp.MustCompile(`(?i)\b(x{2,}|\*{2,})\s*\d{4}\b|\bcard\s+(ending\s+)?\d{4}\b`)
	rePOS     = regexp.MustCompile(`(?i)\b(pos|term|terminal|ref|auth|trace|tid|seq)\s*#?\s*[a-z0-9]{3,}\b`)
	reStoreNo = regexp.MustCompile(`(?i)(#|\bstore\s+|\bno\.?\s*)(\d{1,6})\b`)
	reLongNum = regexp.MustCompile(`\b\d{6,}\b`)
)

// Clean breaks a bank description into merchant name, key, processor and store number.
func Clean(raw string) Cleaned {
	s := Sanitize(raw)
	c := Cleaned{Raw: s}
	for _, p := range processors {
		if p.re.MatchString(s) {
			s = p.re.ReplaceAllString(s, "")
			c.Processor = p.name
			break
		}
	}
	if m := reStoreNo.FindStringSubmatch(s); m != nil {
		c.StoreNo = m[2]
	}
	for _, re := range []*regexp.Regexp{reCard, rePhone, reDate, rePOS, reStoreNo, reLongNum} {
		s = re.ReplaceAllString(s, " ")
	}
	s = strings.Join(strings.Fields(s), " ")
	c.Name = Prettify(s)
	c.Key = Normalize(c.Name)
	return c
}

// Trigrams are the character 3-grams of a normalized string, with padding so
// short names still have some.
func Trigrams(s string) map[string]bool {
	s = "  " + Normalize(s) + " "
	out := map[string]bool{}
	r := []rune(s)
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])] = true
	}
	return out
}

// Similarity is the Jaccard similarity of two strings' trigrams, in [0, 1].
func Similarity(a, b string) float64 {
	ta, tb := Trigrams(a), Trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for g := range ta {
		if tb[g] {
			inter++
		}
	}
	return float64(inter) / float64(len(ta)+len(tb)-inter)
}

// ClusterAliases proposes groups of names that probably are one merchant, by
// trigram similarity at or above threshold (single linkage). It only proposes;
// the user merges or splits. Output is deterministic: names sorted, groups
// ordered by their first member, singletons omitted.
func ClusterAliases(names []string, threshold float64) [][]string {
	uniq := map[string]bool{}
	for _, n := range names {
		if k := Normalize(n); k != "" {
			uniq[k] = true
		}
	}
	keys := make([]string, 0, len(uniq))
	for k := range uniq {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parent := make([]int, len(keys))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if Similarity(keys[i], keys[j]) >= threshold {
				parent[find(j)] = find(i)
			}
		}
	}
	groups := map[int][]string{}
	for i, k := range keys {
		groups[find(i)] = append(groups[find(i)], k)
	}
	var out [][]string
	for _, g := range groups {
		if len(g) > 1 {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

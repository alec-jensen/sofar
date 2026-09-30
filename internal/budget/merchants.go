package budget

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

//go:embed merchants.json
var merchantData []byte

type DefaultCategory struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}
type merchantEntry struct {
	Sub   string   `json:"sub"`
	Name  string   `json:"name"`
	Match []string `json:"match"`
}
type incomeEntry struct {
	Stream string   `json:"stream"`
	Match  []string `json:"match"`
}
type directory struct {
	Categories   []DefaultCategory `json:"categories"`
	Merchants    []merchantEntry   `json:"merchants"`
	Keywords     []merchantEntry   `json:"keywords"`
	Income       []incomeEntry     `json:"income"`
	Transfers    []string          `json:"transfers"`
	CardPayments []string          `json:"cardPayments"`
}

// Directory is sofar's built-in knowledge of common merchants and default categories.
var Directory = func() directory {
	var d directory
	if e := json.Unmarshal(merchantData, &d); e != nil {
		panic("merchants.json: " + e.Error())
	}
	return d
}()

// GroupOf returns the top-level group of a default category id.
func GroupOf(sub string) string {
	for _, c := range Directory.Categories {
		if c.ID == sub {
			return c.Group
		}
	}
	return ""
}

// hasWords reports whether pattern appears in a normalized name as whole words.
func hasWords(normalized, pattern string) bool {
	return strings.Contains(" "+normalized+" ", " "+pattern+" ")
}

func longest(normalized string, entries []merchantEntry) (merchantEntry, bool) {
	best, size := merchantEntry{}, 0
	for _, m := range entries {
		for _, p := range m.Match {
			if len(p) > size && hasWords(normalized, p) {
				best, size = m, len(p)
			}
		}
	}
	return best, size > 0
}

// LookupMerchant finds the default category for spending at a merchant: known
// merchants first, then descriptive keywords. The longest matching phrase wins.
func LookupMerchant(name string) (sub, canonical string, ok bool) {
	n := Normalize(name)
	if m, found := longest(n, Directory.Merchants); found {
		return m.Sub, m.Name, true
	}
	if m, found := longest(n, Directory.Keywords); found {
		return m.Sub, "", true
	}
	return "", "", false
}

// LookupIncome suggests how incoming money from this payer should be treated.
func LookupIncome(name string) string {
	n := Normalize(name)
	best, size := "", 0
	for _, e := range Directory.Income {
		for _, p := range e.Match {
			if len(p) > size && hasWords(n, p) {
				best, size = e.Stream, len(p)
			}
		}
	}
	return best
}

func anyWords(name string, patterns []string) bool {
	n := Normalize(name)
	for _, p := range patterns {
		if hasWords(n, p) {
			return true
		}
	}
	return false
}

func IsCardPayment(name string) bool  { return anyWords(name, Directory.CardPayments) }
func IsTransferText(name string) bool { return anyWords(name, Directory.Transfers) }

var (
	processorPrefix = regexp.MustCompile(`(?i)^(sq|sqc|tst|sp|pp|paypal|py|in|dd|bt|ic|pos|clover|goog|google)\s*\*\s*`)
	wordPrefix      = regexp.MustCompile(`(?i)^(master money|mastercard moneysend|visa direct|pos purchase|pos debit|debit card purchase|debit purchase|dbt crd|dbt card|checkcard \d*|check card \d*|visa purchase|purchase authorized on \d+/\d+|recurring payment authorized on \d+/\d+|recurring purchase|ach debit|ach credit|web pmts|point of sale)\s+`)
	directions      = map[string]bool{"n": true, "s": true, "e": true, "w": true, "ne": true, "nw": true, "se": true, "sw": true}
	states          = map[string]bool{}
)

func init() {
	for _, s := range strings.Fields("al ak az ar ca co ct de fl ga hi id il in ia ks ky la me md ma mi mn ms mo mt ne nv nh nj nm ny nc nd oh ok or pa ri sc sd tn tx ut vt va wa wv wi wy dc") {
		states[s] = true
	}
}

// Prettify turns a bank's raw description into a readable merchant name:
// payment-processor prefixes and trailing store numbers or addresses are
// dropped, known merchants get their usual name, and all-caps text is softened.
func Prettify(raw string) string {
	s := strings.Join(strings.Fields(raw), " ")
	for i := 0; i < 3; i++ {
		before := s
		s = strings.TrimSpace(processorPrefix.ReplaceAllString(s, ""))
		s = strings.TrimSpace(wordPrefix.ReplaceAllString(s, ""))
		if s == before {
			break
		}
	}
	if s == "" {
		return strings.TrimSpace(raw)
	}
	words := strings.Fields(s)
	keep := len(words)
	for i := 1; i < len(words); i++ {
		w := strings.Trim(strings.ToLower(words[i]), ".,*#")
		hasDigit := strings.IndexFunc(words[i], unicode.IsDigit) >= 0
		if hasDigit || strings.HasPrefix(words[i], "#") || (i >= 2 && directions[w]) || (i >= 2 && states[w] && i >= len(words)-2) {
			keep = i
			break
		}
	}
	s = strings.TrimRight(strings.Join(words[:keep], " "), " -*#,.")
	if _, canonical, ok := LookupMerchant(s); ok && canonical != "" && !IsTransferText(s) {
		return canonical
	}
	letters, upper := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letters > 3 && upper == letters {
		parts := strings.Fields(strings.ToLower(s))
		for i, p := range parts {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		s = strings.Join(parts, " ")
	}
	return s
}

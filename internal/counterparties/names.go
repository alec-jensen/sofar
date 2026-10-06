package counterparties

import (
	"regexp"
	"sort"
	"strings"
)

// Platform is the payment rail a payer string came from.
type Platform string

// Platforms.
const (
	PlatformZelle   Platform = "zelle"
	PlatformVenmo   Platform = "venmo"
	PlatformCashApp Platform = "cashapp"
	PlatformPayPal  Platform = "paypal"
	PlatformBank    Platform = "bank"
	PlatformOther   Platform = ""
)

var (
	nonWord = regexp.MustCompile(`[^a-z0-9@$' ]+`)
	noise   = map[string]bool{
		"zelle": true, "venmo": true, "cash": true, "app": true, "cashapp": true, "paypal": true, "payment": true, "pmt": true,
		"from": true, "to": true, "transfer": true, "xfer": true, "p2p": true, "ach": true, "credit": true, "deposit": true,
		"online": true, "mobile": true, "received": true, "sent": true, "via": true, "the": true, "inst": true, "xfr": true,
		"id": true, "ref": true, "conf": true, "web": true, "pay": true, "debit": true, "memo": true,
	}
	digits = regexp.MustCompile(`\d`)
)

// PlatformOf guesses the rail from a description.
func PlatformOf(desc string) Platform {
	d := strings.ToLower(desc)
	switch {
	case strings.Contains(d, "zelle"):
		return PlatformZelle
	case strings.Contains(d, "venmo"):
		return PlatformVenmo
	case strings.Contains(d, "cash app") || strings.Contains(d, "cashapp") || strings.Contains(d, "square cash"):
		return PlatformCashApp
	case strings.Contains(d, "paypal"):
		return PlatformPayPal
	case strings.Contains(d, "transfer from") || strings.Contains(d, "transfer to"):
		return PlatformBank
	}
	return PlatformOther
}

// Name is the comparable part of a payer string: whole words and bare initials.
type Name struct {
	Tokens   []string
	Initials []string
}

// Key is the stored alias key: sorted words, then "/" and sorted initials.
func (n Name) Key() string {
	k := strings.Join(n.Tokens, " ")
	if len(n.Initials) > 0 {
		k += "/" + strings.Join(n.Initials, "")
	}
	return k
}

// Empty reports whether there is nothing to compare.
func (n Name) Empty() bool { return len(n.Tokens) == 0 && len(n.Initials) == 0 }

// ParseKey reads a stored key back.
func ParseKey(key string) Name {
	words, ini, _ := strings.Cut(key, "/")
	var n Name
	n.Tokens = strings.Fields(words)
	for _, r := range ini {
		n.Initials = append(n.Initials, string(r))
	}
	return n
}

// ParseName extracts the person from a bank string such as "VENMO PAYMENT FROM
// SMITH,JOHN Q", "Zelle from John S" or "@dadj". It drops platforms, filler
// words, digits and masks, and puts the words in sorted order so "Smith, John"
// and "John Smith" are the same name. The string is untrusted input and is
// never logged.
func ParseName(desc string) Name {
	d := strings.ToLower(desc)
	d = nonWord.ReplaceAllString(d, " ")
	var n Name
	seen := map[string]bool{}
	for _, w := range strings.Fields(d) {
		w = strings.Trim(w, "'")
		if w == "" || noise[w] || digits.MatchString(w) && !strings.HasPrefix(w, "@") {
			continue
		}
		if len(w) == 1 {
			n.Initials = append(n.Initials, w)
			continue
		}
		if !seen[w] {
			seen[w] = true
			n.Tokens = append(n.Tokens, w)
		}
	}
	sort.Strings(n.Tokens)
	sort.Strings(n.Initials)
	// a middle initial next to a full name is noise; a lone initial is the mask ("JOHN S")
	if len(n.Tokens) >= 2 {
		n.Initials = nil
	}
	return n
}

func tokenMatch(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) >= 2 && strings.HasPrefix(b, a) || len(b) >= 2 && strings.HasPrefix(a, b) {
		return true
	}
	return false
}

// Similarity compares a parsed payer string with a stored alias: 1 for the same
// name, 0.7 for a truncation or a masked surname that fits, 0 otherwise.
func Similarity(q, a Name) float64 {
	if q.Empty() || a.Empty() {
		return 0
	}
	if q.Key() == a.Key() {
		return 1
	}
	short, long := q, a
	if len(q.Tokens) > len(a.Tokens) {
		short, long = a, q
	}
	if len(short.Tokens) == 0 {
		return 0
	}
	used := map[int]bool{}
	for _, t := range short.Tokens {
		ok := false
		for i, u := range long.Tokens {
			if !used[i] && tokenMatch(t, u) {
				used[i], ok = true, true
				break
			}
		}
		if !ok {
			return 0
		}
	}
	// initials on either side must fit a remaining word of the other (or an equal initial)
	for _, side := range [][2]Name{{short, long}, {long, short}} {
		for _, ini := range side[0].Initials {
			ok := false
			for i, u := range side[1].Tokens {
				if !used[i] && strings.HasPrefix(u, ini) {
					used[i], ok = true, true
					break
				}
			}
			for _, j := range side[1].Initials {
				ok = ok || j == ini
			}
			if !ok {
				return 0
			}
		}
	}
	return 0.7
}

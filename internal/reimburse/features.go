// Package reimburse decides what an incoming payment is paying back (S12a, S12b).
// It abstains when unsure, never links on a guess, learns only from the user's
// own confirmations, and keeps every decision reproducible. Links it creates
// are tentative and wait for review; only two cases may link without a human
// decision at all: an exact receivable match and a proven pattern.
package reimburse

import (
	"fmt"
	"math"
	"strings"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

// Pay is the incoming payment side of a pair.
type Pay struct {
	ID             string
	Account        string
	Date           civil.Date // transaction date when known, else posted
	Amount         int64      // cents, positive
	Text           string     // memo and description, lowercase
	CounterpartyID string
	PayerKnown     bool // the payer is a known reimburser
	// ShareRatio is the payer's learned typical share, 0 if unknown.
	ShareRatio float64
	// ShareSpread is how much their share varies (tips, rounding): it widens the tolerance.
	ShareSpread float64
	// Categories counts the confirmed paybacks per expense category.
	Categories map[string]int
	// Paybacks is how many confirmed paybacks this payer has.
	Paybacks int
}

// Cand is the expense side: an expense with money left to be paid back.
type Cand struct {
	ID          string
	Account     string
	Date        civil.Date // transaction date when known, else posted
	Remaining   int64      // cents left to reimburse
	Total       int64      // cents of the whole expense (or split part)
	Category    string
	Merchant    string // normalized merchant key
	Text        string // lowercase description
	Pending     bool
	PartNo      *int
	Receivable  string // an open receivable on it, if any
	ReceivedFor int64  // cents of the receivable still owed
	// PaidBySamePayer is set when this payer already paid part of this expense: a
	// second payback on the same expense is unusual.
	PaidBySamePayer bool
}

// Features describe one (payment, expense) pair. They are stored with every
// decision so it can be replayed.
type Features struct {
	Ratio         float64 `json:"ratio"`          // payment over the expense total
	CleanFraction float64 `json:"clean_fraction"` // 1/2, 1/3, 1/4, 1/5 or 1 when the ratio is close; else 0
	Round         bool    `json:"round"`
	LagDays       int     `json:"lag_days"` // payment date minus expense date
	LagOK         bool    `json:"lag_ok"`
	TooEarly      bool    `json:"too_early"` // the expense is dated after the payment beyond the posting delay
	Food          bool    `json:"food"`
	MemoMatch     bool    `json:"memo_match"`
	SameMerchant  bool    `json:"same_merchant"`
	PayerKnown    bool    `json:"payer_known"`
	PayerShare    bool    `json:"payer_share"` // the ratio matches the payer's learned share
	Over          bool    `json:"over"`        // the payment is more than what is left
	PriorPayer    bool    `json:"prior_payer"` // this payer already paid back part of it
	CatSeen       bool    `json:"cat_seen"`    // this payer has paid back this kind of expense before
	CatNew        bool    `json:"cat_new"`     // an experienced payer who has never paid back this kind
	Receivable    bool    `json:"receivable"`
	Candidates    int     `json:"candidates"`
	// Weak marks a candidate that only fits by amount and time: it is shown in the
	// link picker but never suggested.
	Weak bool `json:"weak"`
}

// Version of the feature set and starter weights.
const ModelVersion = 1

var foodish = map[string]bool{"dining_out": true, "groceries": true, "auto_transit": true, "entertainment": true, "travel": true, "gifts_donations": true}

var fractions = []float64{1, 0.5, 1.0 / 3, 0.25, 2.0 / 3}

// FeatureCtx carries what depends on more than the pair.
type FeatureCtx struct {
	PostingDelayDays int // how long this account's purchases usually take to post
	LookbackDays     int
	Candidates       int
	// FractionTolerance is how far a ratio may sit from a clean fraction (tips, tax); learned per payer.
	FractionTolerance float64
}

// Extract builds the features of a pair.
func Extract(p Pay, c Cand, x FeatureCtx) Features {
	if x.FractionTolerance == 0 {
		x.FractionTolerance = 0.04
	}
	if t := 2 * p.ShareSpread; t > x.FractionTolerance {
		x.FractionTolerance = math.Min(t, 0.12)
	}
	if x.PostingDelayDays == 0 {
		x.PostingDelayDays = 3
	}
	if x.LookbackDays == 0 {
		x.LookbackDays = 30
	}
	f := Features{Candidates: x.Candidates, PayerKnown: p.PayerKnown, Receivable: c.Receivable != "", PriorPayer: c.PaidBySamePayer}
	if c.Total > 0 {
		f.Ratio = float64(p.Amount) / float64(c.Total)
	}
	f.Over = p.Amount > c.Remaining
	for _, fr := range fractions {
		if math.Abs(f.Ratio-fr) <= x.FractionTolerance {
			f.CleanFraction = fr
			break
		}
	}
	f.Round = p.Amount%100 == 0
	f.LagDays = p.Date.DaysSince(c.Date)
	switch {
	case f.LagDays >= 0:
		f.LagOK = f.LagDays <= x.LookbackDays
	default:
		// the expense is dated after the payment: a card purchase often posts after a P2P
		// payback, so this is not penalized up to the account's posting delay (S12a)
		f.TooEarly = -f.LagDays > x.PostingDelayDays
		f.LagOK = !f.TooEarly
	}
	f.Food = foodish[c.Category]
	f.MemoMatch = memoMatches(p.Text, c.Merchant, c.Text)
	f.SameMerchant = c.Merchant != "" && strings.Contains(squash(p.Text), squash(c.Merchant)) && len(c.Merchant) >= 4
	if p.ShareRatio > 0 && math.Abs(f.Ratio-p.ShareRatio) <= x.FractionTolerance {
		f.PayerShare = true
	}
	if n := p.Categories[c.Category]; n >= 2 {
		f.CatSeen = true
	} else if p.Paybacks >= 3 && n == 0 && c.Category != "" {
		f.CatNew = true
	}
	f.Weak = !(f.CleanFraction > 0 || f.MemoMatch || f.SameMerchant || f.PayerShare || f.Receivable)
	return f
}

func squash(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "") }

// memoMatches reports a shared word between a payment's text and the merchant or
// description. Emoji count as words; an empty memo contributes nothing.
func memoMatches(memo, merchant, desc string) bool {
	memo = strings.TrimSpace(strings.ToLower(memo))
	if memo == "" {
		return false
	}
	words := map[string]bool{}
	for _, w := range tokens(merchant + " " + desc) {
		words[w] = true
	}
	for _, w := range tokens(memo) {
		if words[w] {
			return true
		}
	}
	return false
}

// tokens splits on anything that is not a letter or digit; a symbol character
// (an emoji) is its own token.
func tokens(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			out = append(out, string(cur))
		}
		cur = nil
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			cur = append(cur, r)
		case r > 0x2000: // emoji and symbols
			flush()
			out = append(out, string(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

// Starter prior weights (S12b cold start): they only ever produce suggestions.
var weights = struct {
	Bias, Clean, Round, LagNear, LagMid, LagFar, TooEarly, Food, Memo, Merchant, Payer, Share, Receivable, Crowded, PriorPayer, CatSeen, CatNew float64
}{CatSeen: 0.8, CatNew: -1.0, PriorPayer: -4.0, Bias: -2.2, Clean: 1.6, Round: 0.4, LagNear: 1.0, LagMid: 0.5, LagFar: -0.5, TooEarly: -3, Food: 1.2, Memo: 1.5, Merchant: 3, Payer: 1.0, Share: 1.5, Receivable: 4, Crowded: -0.3}

func sigmoid(z float64) float64 { return 1 / (1 + math.Exp(-z)) }

// Score turns features into a probability and plain reasons. An over-large
// payment scores zero: a link may not exceed what is left.
func Score(f Features, pay int64, c Cand) (float64, []string) {
	if f.Over {
		return 0, []string{"more than is left to pay back"}
	}
	w := weights
	z := w.Bias
	var why []string
	if f.CleanFraction > 0 {
		z += w.Clean
		why = append(why, fmt.Sprintf("%s of %s", pct(f.Ratio), dollars(c.Total)))
	}
	if f.Round {
		z += w.Round
	}
	switch {
	case f.TooEarly:
		z += w.TooEarly
	case f.LagDays >= 0 && f.LagDays <= 3:
		z += w.LagNear
		why = append(why, lagText(f.LagDays))
	case f.LagDays > 3 && f.LagDays <= 7:
		z += w.LagMid
		why = append(why, lagText(f.LagDays))
	case f.LagDays > 14:
		z += w.LagFar
	}
	if f.Food {
		z += w.Food
		why = append(why, "a kind of expense that is often shared")
	}
	if f.MemoMatch {
		z += w.Memo
		why = append(why, "the memo mentions it")
	}
	if f.SameMerchant {
		z += w.Merchant
		why = append(why, "the same merchant")
	}
	if f.PayerKnown {
		z += w.Payer
		why = append(why, "this payer has paid you back before")
	}
	if f.PayerShare {
		z += w.Share
		why = append(why, "their usual share")
	}
	if f.Receivable {
		z += w.Receivable
		why = append(why, "it is what they owe you")
	}
	if f.Candidates >= 4 {
		z += w.Crowded
	}
	if f.PriorPayer {
		z += w.PriorPayer
	}
	if f.CatSeen {
		z += w.CatSeen
		why = append(why, "a kind of expense they have paid back before")
	}
	if f.CatNew {
		z += w.CatNew
	}
	return sigmoid(z), why
}

func pct(r float64) string { return fmt.Sprintf("%d%%", int(math.Round(r*100))) }

func dollars(c int64) string { return money.Cents(c).String() }

func lagText(d int) string {
	switch d {
	case 0:
		return "paid the same day"
	case 1:
		return "paid the next day"
	}
	return fmt.Sprintf("paid %d days later", d)
}

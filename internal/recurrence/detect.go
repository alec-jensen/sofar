package recurrence

import (
	"math"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/normalize"
)

// DetectConfig holds the tunable defaults (docs/config-defaults.md).
type DetectConfig struct {
	// Threshold is how many occurrences make a candidate (S14, default 3).
	Threshold int
	// AmountToleranceBP is how far amounts may stray from the median and still count as
	// consistent, in basis points (default 2000 = 20%). Beyond it the item is "variable".
	AmountToleranceBP int64
	// AfterOneKinds are merchant kinds that may be suggested after a single
	// charge (carried over from the legacy app): rent, utilities, phone,
	// insurance, loans, gyms, subscriptions.
	AfterOneKinds map[string]bool
}

// DefaultConfig is the shipped default.
func DefaultConfig() DetectConfig {
	return DetectConfig{
		Threshold:         3,
		AmountToleranceBP: 2000,
		AfterOneKinds: map[string]bool{
			"rent_mortgage": true, "utilities": true, "internet_phone": true, "insurance": true,
			"loan_payments": true, "memberships": true, "other_subscriptions": true,
		},
	}
}

// Txn is the slice of a transaction detection needs.
type Txn struct {
	ID          string
	Date        civil.Date
	Amount      money.Cents // absolute value
	Inflow      bool
	Description string
}

// ItemKind is what a recurring thing is.
type ItemKind string

const (
	ItemBill         ItemKind = "bill"
	ItemSubscription ItemKind = "subscription"
	ItemIncome       ItemKind = "income"
	ItemPaymentPlan  ItemKind = "payment_plan"
)

// Candidate is a possible recurring item. Detection only proposes; the user
// confirms (S5).
type Candidate struct {
	Key         string
	Name        string
	Kind        ItemKind
	CategoryID  string // a suggestion from the merchant directory, may be ""
	Schedule    Schedule
	Amount      money.Cents // median
	Variable    bool
	Occurrences int
	TxnIDs      []string
	LastDate    civil.Date
	NextDate    civil.Date
	AfterOne    bool // suggested after one charge because of what the merchant is
}

type group struct {
	key    string
	inflow bool
	txns   []Txn
}

// Detect finds recurring candidates in a set of transactions. It infers the
// interval from the median gap and requires cfg.Threshold occurrences with
// consistent timing; merchants of a known recurring kind may be suggested after one.
func Detect(txns []Txn, cfg DetectConfig) []Candidate {
	if cfg.Threshold < 2 {
		cfg.Threshold = 2
	}
	groups := map[string]*group{}
	var keys []string
	for _, t := range txns {
		k := normalize.MerchantKey(t.Description)
		if k == "" {
			continue
		}
		gk := k
		if t.Inflow {
			gk = "in:" + k
		}
		g := groups[gk]
		if g == nil {
			g = &group{key: k, inflow: t.Inflow}
			groups[gk] = g
			keys = append(keys, gk)
		}
		g.txns = append(g.txns, t)
	}
	sort.Strings(keys)
	var out []Candidate
	for _, gk := range keys {
		if c, ok := candidate(groups[gk], cfg); ok {
			out = append(out, c)
		}
	}
	return out
}

func median(xs []int) int {
	s := append([]int(nil), xs...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianCents(xs []money.Cents) money.Cents {
	s := append([]money.Cents(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func candidate(g *group, cfg DetectConfig) (Candidate, bool) {
	ts := append([]Txn(nil), g.txns...)
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].Date != ts[j].Date {
			return ts[i].Date.Before(ts[j].Date)
		}
		return ts[i].ID < ts[j].ID
	})
	// two charges within 2 days count as one occurrence (split deposits, double postings)
	var obs []Observed
	byID := map[string]Txn{}
	for _, t := range ts {
		obs = append(obs, Observed{ID: t.ID, Date: t.Date})
		byID[t.ID] = t
	}
	var dates []civil.Date
	var amounts []money.Cents
	var ids []string
	for _, grp := range GroupSplits(obs, 2) {
		var total money.Cents
		for _, o := range grp {
			total += byID[o.ID].Amount
			ids = append(ids, o.ID)
		}
		dates = append(dates, grp[0].Date)
		amounts = append(amounts, total)
	}
	c := Candidate{Key: g.key, Name: normalize.Prettify(ts[len(ts)-1].Description), Occurrences: len(dates), TxnIDs: ids, LastDate: dates[len(dates)-1], Amount: medianCents(amounts)}
	c.Kind = ItemBill
	if g.inflow {
		c.Kind = ItemIncome
	} else if sub, _, ok := normalize.LookupMerchant(ts[len(ts)-1].Description); ok {
		c.CategoryID = normalize.StableCategory(sub)
		switch c.CategoryID {
		case "other_subscriptions", "memberships", "streaming_media", "software_cloud":
			c.Kind = ItemSubscription
		case "groceries", "dining_out", "fuel", "shopping", "home_household", "entertainment", "travel", "personal_care", "gifts_donations", "auto_transit", "other_spending":
			return Candidate{}, false // everyday spending that happens to repeat is not a bill
		}
	}

	if len(dates) == 1 {
		if g.inflow || !cfg.AfterOneKinds[c.CategoryID] {
			return Candidate{}, false
		}
		c.AfterOne = true
		c.Schedule = monthlyOn(dates[0])
		c.NextDate = nextAfter(c.Schedule, dates[0])
		return c, true
	}
	if len(dates) < cfg.Threshold && !(len(dates) >= 2 && cfg.AfterOneKinds[c.CategoryID] && !g.inflow) {
		return Candidate{}, false
	}

	gaps := make([]int, 0, len(dates)-1)
	for i := 1; i < len(dates); i++ {
		gaps = append(gaps, dates[i].DaysSince(dates[i-1]))
	}
	med := median(gaps)
	if med < 1 {
		return Candidate{}, false
	}
	// timing must be consistent: every gap within 25% (at least 3 days) of the median
	slack := int(math.Max(3, float64(med)*0.25))
	for _, gp := range gaps {
		if abs(gp-med) > slack {
			return Candidate{}, false
		}
	}
	c.Schedule = scheduleFor(med, dates)
	c.NextDate = nextAfter(c.Schedule, c.LastDate)
	for _, a := range amounts {
		if money.Cents(abs64(int64(a-c.Amount))*10000) > money.Cents(int64(c.Amount)*cfg.AmountToleranceBP) {
			c.Variable = true
		}
	}
	return c, true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// scheduleFor builds the schedule that fits a median gap, anchored on the observed dates.
func scheduleFor(gap int, dates []civil.Date) Schedule {
	last := dates[len(dates)-1]
	switch {
	case gap >= 27 && gap <= 35:
		return monthlyOn(medianDay(dates))
	case gap >= 85 && gap <= 95:
		s := monthlyOn(last)
		s.Calendar.EveryMonths = 3
		s.Anchor = last
		return s
	case gap >= 350 && gap <= 380:
		s := monthlyOn(last)
		s.Calendar.EveryMonths = 12
		s.Anchor = last
		return s
	}
	return Schedule{Kind: KindInterval, IntervalDays: gap, Anchor: last}
}

func monthlyOn(d civil.Date) Schedule {
	return Schedule{Kind: KindCalendar, Anchor: d, Calendar: CalendarRule{Kind: Monthly, Day: d.D}}
}

func medianDay(dates []civil.Date) civil.Date {
	days := make([]int, len(dates))
	for i, d := range dates {
		days[i] = d.D
	}
	m := median(days)
	last := dates[len(dates)-1]
	return civil.Date{Y: last.Y, M: last.M, D: m}
}

func nextAfter(s Schedule, d civil.Date) civil.Date {
	occ := s.Occurrences(d.AddDays(1), d.AddDays(800), time.UTC)
	if len(occ) == 0 {
		return civil.Date{}
	}
	return occ[0].Date
}

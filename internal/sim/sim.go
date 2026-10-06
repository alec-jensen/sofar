// Package sim generates a seeded synthetic financial life: paychecks, bills,
// subscriptions, ambiguous merchants, P2P paybacks, pending-then-posted card
// purchases, duplicate and rotated ids, and outages, together with the ground
// truth a user would label with. Tests and quality gates for categorization,
// matching and forecasting reuse it (architecture A9.4). The same Config always
// yields the same Life.
package sim

import (
	"fmt"
	"math/rand"
	"sort"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

// Config controls a run.
type Config struct {
	Seed   int64
	Start  civil.Date
	Months int
	// WindowDays is how far back each pull reaches (the Bridge window).
	WindowDays int
	// Outages lists days on which no pull succeeded.
	OutageEvery int // an outage every n days on average; 0 disables
	// DuplicateRate and RotateRate are the share of transactions delivered twice
	// with different ids, or re-keyed in later pulls.
	DuplicateRate float64
	RotateRate    float64
	// TipRate is the share of dining purchases whose posted amount differs from pending.
	TipRate float64
	// PaybackRate is the share of larger dining purchases a friend pays half of; 0 keeps the default of one in six.
	PaybackRate float64
}

// DefaultConfig is a year of life.
func DefaultConfig(seed int64) Config {
	return Config{Seed: seed, Start: civil.Date{Y: 2025, M: 1, D: 1}, Months: 12, WindowDays: 45, OutageEvery: 40, DuplicateRate: 0.004, RotateRate: 0.004, TipRate: 0.6}
}

// Kind is what a transaction really is.
type Kind string

const (
	KindSpending      Kind = "spending"
	KindIncome        Kind = "income"
	KindBill          Kind = "bill"
	KindSubscription  Kind = "subscription"
	KindTransfer      Kind = "transfer"
	KindReimbursement Kind = "reimbursement"
	KindRefund        Kind = "refund"
	KindSideIncome    Kind = "side_income"
	KindCardPayment   Kind = "card_payment"
)

// Truth is the ground truth for one transaction.
type Truth struct {
	Category    string // stable S1a id
	Kind        Kind
	Merchant    string // canonical merchant name, "" for income and transfers
	RecurringID string // the bill, subscription or income stream, if any
	Reimburses  string // for a P2P payback: the id of the expense it repays
	PaybackOf   int64  // cents of the expense expected back, on the expense itself
}

// Raw is what the provider delivers.
type Raw struct {
	ID          string
	Account     string
	Posted      civil.Date
	Amount      money.Cents // negative is an outflow
	Description string
	Payee       string
	Pending     bool
}

// Txn pairs a raw row with its truth and its lifecycle.
type Txn struct {
	Raw   Raw
	Truth Truth
	// Appears is the first day the row is visible (pending rows show up the day
	// of purchase); Posted is when it settles; PendingAmount differs when a tip is added.
	Appears       civil.Date
	PendingAmount money.Cents
	// Rekeyed is the day after which the provider reports a new id; NewID is that id.
	Rekeyed civil.Date
	NewID   string
	// DuplicateOf is set on a duplicate row: the id of the original.
	DuplicateOf string
}

// Pull is one successful provider response.
type Pull struct {
	Day  civil.Date
	Hour int
	Rows []Raw
}

// Life is a generated history.
type Life struct {
	Config Config
	Txns   []Txn
	Pulls  []Pull
}

type merchant struct {
	name       string
	descriptor func(r *rand.Rand) string
	weights    []catWeight
	minC, maxC int64 // cents
	dining     bool
	weight     int
}

type catWeight struct {
	category string
	weight   int
	// minAmount and maxAmount, in cents, narrow when this category applies to an ambiguous merchant.
	minAmount, maxAmount int64
}

var storeNo = func(r *rand.Rand) string { return fmt.Sprintf("%d", 100+r.Intn(900)) }

func plain(name string) func(*rand.Rand) string {
	return func(r *rand.Rand) string { return name }
}

func withStore(prefix, name string) func(*rand.Rand) string {
	return func(r *rand.Rand) string { return fmt.Sprintf("%s%s #%s ARLINGTON TX", prefix, name, storeNo(r)) }
}

var merchants = []merchant{
	{name: "H-E-B", descriptor: withStore("", "HEB"), weights: []catWeight{{"groceries", 1, 0, 0}}, minC: 2500, maxC: 18000, weight: 14},
	{name: "Kroger", descriptor: withStore("", "KROGER"), weights: []catWeight{{"groceries", 1, 0, 0}}, minC: 2000, maxC: 15000, weight: 6},
	{name: "Starbucks", descriptor: withStore("SQ *", "STARBUCKS"), weights: []catWeight{{"dining_out", 1, 0, 0}}, minC: 450, maxC: 1400, dining: true, weight: 12},
	{name: "Chipotle", descriptor: withStore("", "CHIPOTLE"), weights: []catWeight{{"dining_out", 1, 0, 0}}, minC: 900, maxC: 2200, dining: true, weight: 8},
	{name: "Torchy's Tacos", descriptor: withStore("TST* ", "TORCHYS TACOS"), weights: []catWeight{{"dining_out", 1, 0, 0}}, minC: 1100, maxC: 3200, dining: true, weight: 6},
	{name: "Shell", descriptor: withStore("", "SHELL OIL"), weights: []catWeight{{"fuel", 1, 0, 0}}, minC: 2500, maxC: 7500, weight: 6},
	{name: "Chevron", descriptor: withStore("", "CHEVRON"), weights: []catWeight{{"fuel", 3, 2500, 7500}, {"dining_out", 1, 300, 1200}}, minC: 300, maxC: 7500, weight: 5},
	{name: "Uber", descriptor: plain("UBER   *TRIP HELP.UBER.COM"), weights: []catWeight{{"auto_transit", 1, 0, 0}}, minC: 800, maxC: 4000, weight: 3},
	{name: "Amazon", descriptor: func(r *rand.Rand) string { return fmt.Sprintf("AMZN Mktp US*%X", r.Intn(65535)) }, weights: []catWeight{{"shopping", 5, 1500, 12000}, {"home_household", 3, 800, 6000}, {"groceries", 1, 2500, 9000}}, minC: 800, maxC: 12000, weight: 10},
	{name: "Walmart", descriptor: withStore("WAL-MART ", "SUPERCENTER"), weights: []catWeight{{"groceries", 4, 3000, 16000}, {"shopping", 2, 1500, 9000}, {"home_household", 2, 1000, 7000}}, minC: 1000, maxC: 16000, weight: 8},
	{name: "Target", descriptor: withStore("", "TARGET"), weights: []catWeight{{"shopping", 3, 1500, 9000}, {"home_household", 2, 1000, 6000}, {"groceries", 2, 2000, 8000}}, minC: 1000, maxC: 9000, weight: 6},
	{name: "CVS", descriptor: withStore("", "CVS/PHARMACY"), weights: []catWeight{{"health_medical", 3, 800, 5000}, {"personal_care", 2, 500, 2500}}, minC: 500, maxC: 5000, weight: 3},
	{name: "AMC Theatres", descriptor: withStore("", "AMC THEATRES"), weights: []catWeight{{"entertainment", 1, 0, 0}}, minC: 1500, maxC: 4500, weight: 2},
	{name: "Southwest Airlines", descriptor: plain("SOUTHWEST AIRLINES 5262"), weights: []catWeight{{"travel", 1, 0, 0}}, minC: 15000, maxC: 45000, weight: 1},
	{name: "Petco", descriptor: withStore("", "PETCO"), weights: []catWeight{{"other_spending", 1, 0, 0}}, minC: 1500, maxC: 8000, weight: 2},
	{name: "Great Clips", descriptor: withStore("", "GREAT CLIPS"), weights: []catWeight{{"personal_care", 1, 0, 0}}, minC: 2200, maxC: 3500, weight: 2},
}

type recurring struct {
	id, name, descriptor, category string
	kind                           Kind
	day                            int
	cents                          int64
	variance                       int64
	hikeMonth                      int // month index at which the price rises
	hike                           int64
}

var recurringSet = []recurring{
	{id: "rent", name: "Landlord", descriptor: "ZELLE PAYMENT LANDLORD LLC", category: "rent_mortgage", kind: KindBill, day: 1, cents: 135000},
	{id: "power", name: "Reliant Energy", descriptor: "RELIANT ENERGY PAYMENT", category: "utilities", kind: KindBill, day: 12, cents: 9500, variance: 5000},
	{id: "internet", name: "Spectrum", descriptor: "SPECTRUM INTERNET 8883", category: "internet_phone", kind: KindBill, day: 16, cents: 7499},
	{id: "phone", name: "Verizon", descriptor: "VERIZON WIRELESS PAYMENT", category: "internet_phone", kind: KindBill, day: 20, cents: 8500},
	{id: "insurance", name: "State Farm", descriptor: "STATE FARM INSURANCE", category: "insurance", kind: KindBill, day: 5, cents: 12800},
	{id: "netflix", name: "Netflix", descriptor: "NETFLIX.COM LOS GATOS CA", category: "streaming_media", kind: KindSubscription, day: 12, cents: 1549, hikeMonth: 6, hike: 200},
	{id: "spotify", name: "Spotify", descriptor: "SPOTIFY USA", category: "streaming_media", kind: KindSubscription, day: 3, cents: 1199},
	{id: "gym", name: "Planet Fitness", descriptor: "PLANET FITNESS CLUB FEE", category: "memberships", kind: KindSubscription, day: 8, cents: 2499},
}

type gen struct {
	cfg  Config
	r    *rand.Rand
	seq  int
	life Life
}

func (g *gen) id() string {
	g.seq++
	return fmt.Sprintf("sim-%06d", g.seq)
}

func (g *gen) add(t Txn) *Txn {
	if t.Appears.IsZero() {
		t.Appears = t.Raw.Posted
	}
	if t.Raw.ID == "" {
		t.Raw.ID = g.id()
	}
	if t.Raw.Account == "" {
		t.Raw.Account = "checking"
	}
	g.life.Txns = append(g.life.Txns, t)
	return &g.life.Txns[len(g.life.Txns)-1]
}

func (g *gen) pickMerchant() merchant {
	total := 0
	for _, m := range merchants {
		total += m.weight
	}
	n := g.r.Intn(total)
	for _, m := range merchants {
		if n < m.weight {
			return m
		}
		n -= m.weight
	}
	return merchants[0]
}

func (g *gen) amount(minC, maxC int64) int64 {
	if maxC <= minC {
		return minC
	}
	// skew toward smaller amounts
	u := g.r.Float64()
	return minC + int64(u*u*float64(maxC-minC))
}

// Generate builds the life for a config.
func Generate(cfg Config) Life {
	if cfg.WindowDays == 0 {
		cfg.WindowDays = 45
	}
	g := &gen{cfg: cfg, r: rand.New(rand.NewSource(cfg.Seed))}
	g.life.Config = cfg
	end := civil.AddMonthsClamped(cfg.Start, cfg.Months)
	months := cfg.Months

	// income streams
	for d := cfg.Start; d.Before(end); d = d.AddDays(1) {
		if d.DaysSince(cfg.Start)%14 == 4 { // biweekly paycheck
			amt := int64(212000)
			if d.DaysSince(cfg.Start) > 180 {
				amt = 224000 // a raise
			}
			g.add(Txn{Raw: Raw{Posted: d, Amount: money.Cents(amt), Description: "ACME CORP PAYROLL DIRECT DEP", Payee: "ACME CORP"}, Truth: Truth{Category: "salary", Kind: KindIncome, RecurringID: "salary"}})
		}
	}
	for m := 0; m < months; m++ {
		ym := cfg.Start.Month().AddMonths(m)
		// irregular side income roughly every other month
		if g.r.Intn(2) == 0 {
			day := civil.Date{Y: ym.Y, M: ym.M, D: 1 + g.r.Intn(27)}
			g.add(Txn{Raw: Raw{Posted: day, Amount: money.Cents(15000 + g.r.Int63n(60000)), Description: "STRIPE TRANSFER SIDE GIG", Payee: "STRIPE"}, Truth: Truth{Category: "self_employment", Kind: KindSideIncome}})
		}
		// bills and subscriptions
		for _, rc := range recurringSet {
			cents := rc.cents
			if rc.hikeMonth > 0 && m >= rc.hikeMonth {
				cents += rc.hike
			}
			if rc.variance > 0 {
				cents += g.r.Int63n(rc.variance)
			}
			day := civil.Date{Y: ym.Y, M: ym.M, D: rc.day}
			g.add(Txn{Raw: Raw{Posted: day, Amount: money.Cents(-cents), Description: rc.descriptor, Payee: rc.name}, Truth: Truth{Category: rc.category, Kind: rc.kind, Merchant: rc.name, RecurringID: rc.id}})
		}
		if ym.M == 3 { // an annual bill
			g.add(Txn{Raw: Raw{Posted: civil.Date{Y: ym.Y, M: ym.M, D: 10}, Amount: -9500, Description: "TX DMV REGISTRATION", Payee: "TX DMV"}, Truth: Truth{Category: "auto_transit", Kind: KindBill, Merchant: "TX DMV", RecurringID: "dmv"}})
		}
		// monthly card payment
		g.add(Txn{Raw: Raw{Posted: civil.Date{Y: ym.Y, M: ym.M, D: 25}, Amount: -45000, Description: "CHASE CREDIT CRD AUTOPAY", Payee: "CHASE"}, Truth: Truth{Category: "credit_card_payment", Kind: KindCardPayment}})
	}

	// daily spending, with pending purchases, tips, P2P and refunds
	for d := cfg.Start; d.Before(end); d = d.AddDays(1) {
		n := 0
		for p := g.r.Float64(); p < 1.0 && n < 5; p = g.r.Float64() * 0.6 {
			n++
			if g.r.Float64() > 0.55 {
				break
			}
		}
		for i := 0; i < n; i++ {
			g.spend(d)
		}
	}
	g.finish(end)
	return g.life
}

func (g *gen) spend(d civil.Date) {
	m := g.pickMerchant()
	cat := m.weights[0].category
	lo, hi := m.minC, m.maxC
	if len(m.weights) > 1 {
		total := 0
		for _, w := range m.weights {
			total += w.weight
		}
		n := g.r.Intn(total)
		for _, w := range m.weights {
			if n < w.weight {
				cat, lo, hi = w.category, w.minAmount, w.maxAmount
				break
			}
			n -= w.weight
		}
	}
	amount := g.amount(lo, hi)
	t := Txn{Raw: Raw{Posted: d.AddDays(1 + g.r.Intn(2)), Amount: money.Cents(-amount), Description: m.descriptor(g.r), Payee: m.name}, Appears: d, PendingAmount: money.Cents(-amount),
		Truth: Truth{Category: cat, Kind: KindSpending, Merchant: m.name}}
	if m.dining && g.r.Float64() < g.cfg.TipRate {
		tip := amount * int64(15+g.r.Intn(11)) / 100
		t.Raw.Amount = money.Cents(-(amount + tip))
	}
	added := g.add(t)
	// a friend pays part of a meal back a few days later
	if m.dining && amount > 1800 && g.payback() {
		back := (amount / 2 / 100) * 100
		g.add(Txn{Raw: Raw{Posted: d.AddDays(2 + g.r.Intn(4)), Amount: money.Cents(back), Description: "VENMO PAYMENT FROM DAD", Payee: "DAD"},
			Truth: Truth{Category: "reimbursement", Kind: KindReimbursement, Reimburses: added.Raw.ID}})
		added.Truth.PaybackOf = back
	}
	// an occasional refund
	if g.r.Intn(80) == 0 {
		g.add(Txn{Raw: Raw{Posted: d.AddDays(5 + g.r.Intn(8)), Amount: money.Cents(amount), Description: m.descriptor(g.r) + " REFUND", Payee: m.name}, Truth: Truth{Category: "refund", Kind: KindRefund, Merchant: m.name}})
	}
}

// finish builds the provider pulls: twice a day, each showing everything
// visible inside the window, with duplicates, rotated ids and outages.
func (g *gen) finish(end civil.Date) {
	sort.SliceStable(g.life.Txns, func(i, j int) bool {
		a, b := g.life.Txns[i], g.life.Txns[j]
		if a.Raw.Posted != b.Raw.Posted {
			return a.Raw.Posted.Before(b.Raw.Posted)
		}
		return a.Raw.ID < b.Raw.ID
	})
	for i := range g.life.Txns {
		t := &g.life.Txns[i]
		if g.r.Float64() < g.cfg.RotateRate {
			t.Rekeyed = t.Raw.Posted.AddDays(3 + g.r.Intn(10))
			t.NewID = fmt.Sprintf("rot-%06d", i)
		}
	}
	// duplicates are separate rows with their own ids
	n := len(g.life.Txns)
	for i := 0; i < n; i++ {
		if g.r.Float64() < g.cfg.DuplicateRate {
			dup := g.life.Txns[i]
			dup.DuplicateOf = dup.Raw.ID
			dup.Raw.ID = g.id()
			dup.Truth = g.life.Txns[i].Truth
			g.life.Txns = append(g.life.Txns, dup)
		}
	}
	for d := g.cfg.Start; d.Before(end); d = d.AddDays(1) {
		if g.cfg.OutageEvery > 0 && g.r.Intn(g.cfg.OutageEvery) == 0 {
			continue // an outage: neither pull succeeded
		}
		for _, hour := range []int{6, 18} {
			p := Pull{Day: d, Hour: hour}
			for _, t := range g.life.Txns {
				if t.Appears.After(d) || d.DaysSince(t.Appears) > g.cfg.WindowDays {
					continue
				}
				row := t.Raw
				if !t.Rekeyed.IsZero() && !t.Rekeyed.After(d) {
					row.ID = t.NewID
				}
				if t.Raw.Posted.After(d) {
					row.Pending, row.Posted, row.Amount = true, d, t.PendingAmount
				}
				p.Rows = append(p.Rows, row)
			}
			g.life.Pulls = append(g.life.Pulls, p)
		}
	}
}

// Oracle labels a transaction the way a real user would: usually right, wrong
// with probability errRate (choosing another category at random).
type Oracle struct {
	r       *rand.Rand
	errRate float64
	cats    []string
}

// NewOracle returns a user oracle. Errors are drawn from the same seed, so runs repeat.
func NewOracle(seed int64, errRate float64) *Oracle {
	cats := []string{"groceries", "dining_out", "fuel", "shopping", "home_household", "entertainment", "other_spending", "health_medical", "personal_care", "travel", "auto_transit"}
	return &Oracle{r: rand.New(rand.NewSource(seed)), errRate: errRate, cats: cats}
}

// Label returns the user's category for a transaction.
func (o *Oracle) Label(t Txn) string {
	if o.r.Float64() < o.errRate {
		return o.cats[o.r.Intn(len(o.cats))]
	}
	return t.Truth.Category
}

// payback decides whether a friend pays this meal back.
func (g *gen) payback() bool {
	if g.cfg.PaybackRate > 0 {
		return g.r.Float64() < g.cfg.PaybackRate
	}
	return g.r.Intn(6) == 0
}

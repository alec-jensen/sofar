package reimburse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/receivables"
)

var (
	// ErrNotFound means no such payment, link or pattern.
	ErrNotFound = errors.New("reimburse: not found")
	// ErrInvalid means a bad request.
	ErrInvalid = errors.New("reimburse: invalid request")
)

// Config holds the tunable settings (listed in docs/config-defaults.md).
type Config struct {
	SuggestThreshold      float64 // minimum calibrated probability to suggest
	AmbiguityMargin       float64 // best must beat the runner-up by this much
	AutoConfidence        float64 // minimum probability for a proven pattern to link tentatively
	GraceDays             int     // days a payment waits for its expense before asking
	HoldDays              int     // days an unlinked reimbursement stays outside income
	LookbackDays          int
	MaterialityCents      int64 // amounts above this are never auto-tentative
	StreakToGraduate      int
	PatternRatioTolerance float64
	PrecisionFloor        float64
	PostingDelayDays      int
	// AbstainBias raises the suggestion threshold for an account when balances
	// hint that pending items are hidden (S12a item 4). It never creates a link.
	AbstainBias map[string]float64
}

// DefaultConfig is the shipped configuration.
func DefaultConfig() Config {
	return Config{SuggestThreshold: 0.6, AmbiguityMargin: 0.15, AutoConfidence: 0.8, GraceDays: 5, HoldDays: 30, LookbackDays: 30,
		MaterialityCents: 10000, StreakToGraduate: 3, PatternRatioTolerance: 0.04, PrecisionFloor: 0.8, PostingDelayDays: 3, AbstainBias: map[string]float64{}}
}

// HintBias turns the gap between an account's balance and available balance,
// after visible pending items, into an abstain bias (S12a item 4). A positive
// unexplained gap suggests hidden pending holds.
func HintBias(balance, available, visiblePending int64) float64 {
	gap := balance - available - visiblePending
	switch {
	case gap <= 0:
		return 0
	case gap < 1000:
		return 0.05
	}
	return 0.15
}

// Service decides, learns and holds.
type Service struct {
	DB    *sql.DB
	Reg   *events.Registry
	Links *links.Service
	Cps   *counterparties.Service
	Recv  *receivables.Service
	Now   func() time.Time
	Home  *time.Location
	Cfg   Config
}

// New builds a Service and registers its event payloads.
func New(db *sql.DB, reg *events.Registry, l *links.Service, c *counterparties.Service, r *receivables.Service) *Service {
	reg.Register(KindPatternDemoted, 1, PatternDemoted{})
	return &Service{DB: db, Reg: reg, Links: l, Cps: c, Recv: r, Now: time.Now, Home: time.UTC, Cfg: DefaultConfig()}
}

func (s *Service) today() civil.Date { return civil.DateOf(s.Now().In(s.Home)) }

// Kind is what the matcher decided for a payment.
type Kind string

// Decisions.
const (
	KindAuto      Kind = "auto_tentative" // linked tentatively, waiting for a confirm
	KindSuggest   Kind = "suggest"
	KindAmbiguous Kind = "ambiguous" // two fit: abstain and show both
	KindAbstain   Kind = "abstain"
	KindSkip      Kind = "skip" // not a reimbursement (policy, label, or already settled)
)

// Option is one scored candidate for a payment.
type Option struct {
	ExpenseID    string
	ReceivableID string
	Amount       int64
	P            float64
	Why          []string
	Features     Features
}

// Decision is the matcher's verdict on one payment, with its reasoning.
type Decision struct {
	PaymentID string
	Kind      Kind
	Source    string // receivable, pattern, model
	Reason    string // why it won or abstained, in plain words
	Payer     *counterparties.Counterparty
	Chosen    *Option
	Options   []Option // every candidate, strongest first (weak ones included for the debugger)
	PatternID string
}

type world struct {
	payments []row
	expenses []row
	byID     map[string]row
	recv     []receivables.Receivable
	postDays map[string]int
}

func (s *Service) loadWorld(ctx context.Context, q events.DBTX, only string) (*world, error) {
	w := &world{byID: map[string]row{}, postDays: map[string]int{}}
	lo := s.today().AddDays(-(s.Cfg.LookbackDays + 120))
	all, err := queryRows(ctx, q, ` AND r.posted_on >= $1`, lo.String())
	if err != nil {
		return nil, err
	}
	for _, x := range all {
		w.byID[x.ID] = x
		if eligiblePayment(x) && (only == "" && !x.Pending || only == x.ID) {
			w.payments = append(w.payments, x)
		}
		if eligibleExpense(x) {
			w.expenses = append(w.expenses, x)
		}
	}
	if only != "" {
		if p, ok := w.byID[only]; ok && !contains(w.payments, only) && p.Amount > 0 {
			w.payments = append(w.payments, p)
		}
	}
	if w.recv, err = s.Recv.List(ctx, q, true); err != nil {
		return nil, err
	}
	rs, err := q.QueryContext(ctx, `SELECT account_id, coalesce(percentile_disc(0.5) WITHIN GROUP (ORDER BY posted_on - transacted_on), 0)
		FROM raw_transactions WHERE transacted_on IS NOT NULL AND amount < 0 GROUP BY account_id`)
	if err == nil {
		for rs.Next() {
			var a string
			var d int
			rs.Scan(&a, &d)
			if d > 0 {
				w.postDays[a] = d
			}
		}
		rs.Close()
	}
	return w, nil
}

func contains(rs []row, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func logit(p float64) float64 { return math.Log(p / (1 - p)) }

// plan decides every eligible payment (or one) without writing anything. The
// debugger and the writer share it, so what is shown is what would be done.
func (s *Service) plan(ctx context.Context, q events.DBTX, only string) ([]Decision, *world, error) {
	w, err := s.loadWorld(ctx, q, only)
	if err != nil {
		return nil, nil, err
	}
	var out []Decision
	for i, p := range w.payments {
		if only != "" {
			p.Linked = 0 // replay as if it were not linked yet
			w.payments[i] = p
		}
		d := s.decideOne(ctx, q, w, p)
		if d.Kind != KindAuto && d.Kind != KindSkip {
			s.bindDeclaration(ctx, q, w, &d)
		}
		out = append(out, d)
	}
	// global assignment over payments that still have strong options (S12b step 3)
	var rows []int
	var cols []string
	colIdx := map[string]int{}
	for i, d := range out {
		if d.Kind != KindSuggest {
			continue
		}
		rows = append(rows, i)
		for _, o := range d.Options {
			if o.Features.Weak || o.P < s.threshold(w.byID[d.PaymentID].Account) {
				continue
			}
			if _, ok := colIdx[o.ExpenseID]; !ok {
				colIdx[o.ExpenseID] = len(cols)
				cols = append(cols, o.ExpenseID)
			}
		}
	}
	if len(rows) > 0 && len(cols) > 0 {
		const copies = 2
		profit := make([][]float64, len(rows))
		for ri, di := range rows {
			profit[ri] = make([]float64, len(cols)*copies)
			for k := range profit[ri] {
				profit[ri][k] = -1
			}
			thr := logit(s.threshold(w.byID[out[di].PaymentID].Account))
			for _, o := range out[di].Options {
				if ci, ok := colIdx[o.ExpenseID]; ok && !o.Features.Weak && o.P > 0 && o.P < 1 && logit(o.P) > thr {
					for c := 0; c < copies; c++ {
						profit[ri][ci+c*len(cols)] = logit(o.P) - thr - 0.3*float64(c)
					}
				}
			}
		}
		asg := Assign(profit)
		taken := map[string]int64{}
		order := make([]int, len(asg))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			ja, jb := asg[order[a]].Candidate, asg[order[b]].Candidate
			pa, pb := -1e9, -1e9
			if ja >= 0 {
				pa = profit[order[a]][ja]
			}
			if jb >= 0 {
				pb = profit[order[b]][jb]
			}
			return pa > pb
		})
		for _, ri := range order {
			di := rows[ri]
			j := asg[ri].Candidate
			if j < 0 {
				out[di].Kind, out[di].Chosen = KindAbstain, nil
				out[di].Reason = "another payment fits the same expense better, so this one waits"
				continue
			}
			exp := cols[j%len(cols)]
			var opt *Option
			for k := range out[di].Options {
				if out[di].Options[k].ExpenseID == exp {
					opt = &out[di].Options[k]
				}
			}
			e := w.byID[exp]
			if opt == nil || taken[exp]+opt.Amount > -e.Amount-e.Linked {
				out[di].Kind, out[di].Chosen = KindAbstain, nil
				out[di].Reason = "the expense cannot take more than what is left to pay back"
				continue
			}
			taken[exp] += opt.Amount
			out[di].Chosen = opt
		}
	}
	for i := range out {
		if out[i].Kind == KindSuggest && out[i].Chosen != nil {
			s.maybeAuto(ctx, q, w, &out[i])
		}
	}
	return out, w, nil
}

func (s *Service) threshold(account string) float64 {
	return math.Min(0.95, s.Cfg.SuggestThreshold+s.Cfg.AbstainBias[account])
}

// maybeAuto upgrades a suggestion to an auto-tentative link in the one allowed
// pattern case: a proven pattern, enough confidence, small enough amount, and
// a payer without mixed history.
func (s *Service) maybeAuto(ctx context.Context, q events.DBTX, w *world, d *Decision) {
	if d.Payer == nil || d.Chosen == nil || d.Chosen.ReceivableID != "" {
		return
	}
	pay, exp := w.byID[d.PaymentID], w.byID[d.Chosen.ExpenseID]
	if d.Chosen.Amount > s.Cfg.MaterialityCents || d.Chosen.P < s.Cfg.AutoConfidence || d.Payer.Policy == counterparties.PolicyNever {
		return
	}
	if pr, err := s.Profile(ctx, q, d.Payer.ID); err != nil || pr.Mixed {
		return
	}
	ps, err := s.PatternsFor(ctx, q, d.Payer.ID)
	if err != nil {
		return
	}
	ratio := float64(d.Chosen.Amount) / float64(-exp.Amount)
	lag := pay.Date.DaysSince(exp.Date)
	for _, p := range ps {
		if p.State != Active || p.CategoryID != exp.Category || math.Abs(p.ShareRatio-ratio) > s.Cfg.PatternRatioTolerance || lag > p.LagMax+1 {
			continue
		}
		d.Kind, d.Source, d.PatternID = KindAuto, "pattern", p.ID
		d.Reason = "proven pattern: " + p.Describe(d.Payer.Name)
		return
	}
}

// decideOne runs the per-payment stages: policy, receivables first, scored
// candidates, the ambiguity gate. The global assignment follows in plan.
func (s *Service) decideOne(ctx context.Context, q events.DBTX, w *world, p row) Decision {
	d := Decision{PaymentID: p.ID, Kind: KindAbstain}
	m, err := s.Cps.Resolve(ctx, q, p.payerText())
	if err == nil {
		d.Payer = m.Counterparty
	}
	ambiguousPayer := err == nil && m.Ambiguous()
	var label string
	q.QueryRowContext(ctx, `SELECT label FROM payer_labels WHERE txn_id=$1`, p.ID).Scan(&label)
	switch {
	case label == "other":
		d.Kind, d.Reason = KindSkip, "you said this deposit is not a reimbursement"
		return d
	case d.Payer != nil && d.Payer.Policy == counterparties.PolicyNever:
		d.Kind, d.Reason = KindSkip, d.Payer.Name+" is set to never reimbursement"
		return d
	case !isP2P(p, d.Payer != nil) && !ambiguousPayer:
		// not a person paying: only a merchant refund can fit, by merchant name (suggestion only)
	}
	remaining := p.Amount - p.Linked
	pay := Pay{ID: p.ID, Account: p.Account, Date: p.Date, Amount: remaining, Text: p.memoText()}
	if d.Payer != nil {
		pay.CounterpartyID, pay.PayerKnown = d.Payer.ID, true
		if pr, err := s.Profile(ctx, q, d.Payer.ID); err == nil {
			pay.ShareRatio = pr.MedianShare
			pay.PayerKnown = pr.Paybacks > 0 || d.Payer.Policy == counterparties.PolicyAlwaysReimbursment
		}
	}

	// stage 1: open receivables from the same payer get first claim (S12a)
	if opt, why, ok := s.receivableStage(w, p, remaining, d.Payer, ambiguousPayer); ok {
		d.Options = append(d.Options, opt...)
		d.Reason = why
		if len(opt) == 1 && d.Payer != nil && !ambiguousPayer && opt[0].P >= 0.99 {
			d.Kind, d.Source, d.Chosen = KindAuto, "receivable", &d.Options[0]
			return d
		}
		if len(opt) > 0 {
			d.Kind, d.Source, d.Chosen = KindSuggest, "receivable", &d.Options[0]
			d.Options[0].Why = append(d.Options[0].Why, "the oldest of several that fit")
			if len(opt) > 1 {
				d.Kind, d.Chosen = KindAmbiguous, nil
			}
			return d
		}
	}

	// stage 2: score the expenses in the window
	ctxF := FeatureCtx{PostingDelayDays: orDefault(w.postDays[p.Account], s.Cfg.PostingDelayDays), LookbackDays: s.Cfg.LookbackDays}
	var cands []row
	for _, e := range w.expenses {
		if e.Currency != p.Currency || e.ID == p.ID {
			continue
		}
		if age := p.Date.DaysSince(e.Date); age > s.Cfg.LookbackDays || -age > ctxF.PostingDelayDays+30 {
			continue
		}
		cands = append(cands, e)
	}
	ctxF.Candidates = len(cands)
	for _, e := range cands {
		c := Cand{ID: e.ID, Account: e.Account, Date: e.Date, Remaining: -e.Amount - e.Linked, Total: -e.Amount, Category: e.Category, Merchant: e.merchant(), Text: strings.ToLower(e.Description), Pending: e.Pending}
		f := Extract(pay, c, ctxF)
		prob, why := Score(f, remaining, c)
		if !isP2P(p, d.Payer != nil) && !f.SameMerchant {
			continue // a deposit that is not a person is only matched by merchant
		}
		d.Options = append(d.Options, Option{ExpenseID: e.ID, Amount: remaining, P: prob, Why: why, Features: f})
	}
	sort.SliceStable(d.Options, func(i, j int) bool { return d.Options[i].P > d.Options[j].P })

	thr := s.threshold(p.Account)
	var strong []Option
	for _, o := range d.Options {
		if !o.Features.Weak && !o.Features.Over && !o.Features.TooEarly && o.P >= thr {
			strong = append(strong, o)
		}
	}
	switch {
	case len(strong) == 0:
		d.Kind = KindAbstain
		d.Reason = abstainReason(d.Options, thr)
	case len(strong) > 1 && strong[0].P-strong[1].P < s.Cfg.AmbiguityMargin:
		d.Kind = KindAmbiguous
		d.Reason = fmt.Sprintf("two expenses fit about equally well (%.0f%% and %.0f%%), so it asks", strong[0].P*100, strong[1].P*100)
	default:
		d.Kind, d.Source = KindSuggest, "model"
		d.Reason = strings.Join(strong[0].Why, "; ")
		if ambiguousPayer {
			d.Kind, d.Reason = KindAbstain, "the payer's name fits more than one person, so it asks first"
		}
	}
	if d.Kind == KindSuggest {
		d.Chosen = &d.Options[0]
	}
	return d
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func abstainReason(opts []Option, thr float64) string {
	if len(opts) == 0 {
		return "no expense in the window to pay back: it is held until one appears"
	}
	weak := 0
	for _, o := range opts {
		if o.Features.Weak {
			weak++
		}
	}
	if weak == len(opts) {
		return "no candidate has more than an amount and a date in its favor: held until the matching expense appears"
	}
	return fmt.Sprintf("the best candidate scores %.0f%%, under the %.0f%% needed", opts[0].P*100, thr*100)
}

// receivableStage lists open receivables this payment could satisfy. It
// matches by payer and amount within a small tolerance for fees.
func (s *Service) receivableStage(w *world, p row, remaining int64, payer *counterparties.Counterparty, ambiguousPayer bool) ([]Option, string, bool) {
	var out []Option
	var open []receivables.Receivable
	for _, r := range w.recv {
		if !r.Open() {
			continue
		}
		samePayer := payer != nil && r.CounterpartyID != nil && *r.CounterpartyID == payer.ID
		if !samePayer {
			if r.CounterpartyID != nil || ambiguousPayer || payer != nil {
				continue // someone else's debt, or a payer we cannot place
			}
		}
		owed := r.Amount - r.ReceivedAmount
		tol := max(100, owed*3/100)
		hi := owed
		if r.AmountMax != nil {
			hi = *r.AmountMax - r.ReceivedAmount
		}
		if remaining >= owed-tol && remaining <= hi+tol {
			open = append(open, r)
		}
	}
	if len(open) == 0 {
		return nil, "", false
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].OpenedOn.Before(open[j].OpenedOn) })
	for _, r := range open {
		owed := r.Amount - r.ReceivedAmount
		e := w.byID[r.ExpenseID]
		pr := 0.99
		if payer == nil || (r.CounterpartyID == nil) {
			pr = 0.8 // an unknown payer: suggested, never auto
		}
		out = append(out, Option{ExpenseID: r.ExpenseID, ReceivableID: r.ID, Amount: min(remaining, owed), P: pr, Why: []string{"it is what they owe you"},
			Features: Features{Receivable: true, Ratio: float64(remaining) / math.Max(1, float64(-e.Amount)), Candidates: len(open)}})
	}
	return out, "an open receivable fits: receivables get first claim", true
}

// Debug replays the matcher for one payment: payer, receivables, every
// candidate with its features and score, and why it won or abstained. It
// writes nothing, and works for any payment, linked or not.
func (s *Service) Debug(ctx context.Context, q events.DBTX, paymentID string) (Decision, error) {
	ds, _, err := s.plan(ctx, q, paymentID)
	if err != nil {
		return Decision{}, err
	}
	for _, d := range ds {
		if d.PaymentID == paymentID {
			return d, nil
		}
	}
	return Decision{}, ErrNotFound
}

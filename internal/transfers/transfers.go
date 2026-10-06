// Package transfers pairs the two sides of a move between the user's own
// accounts and asks about the one-sided ones (S12, S13). A pair is neutral: it
// never counts as income or spending. Pairing writes ordinary overlay events
// as the system, so every pair can be undone like any other decision.
package transfers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
)

// Categories used for transfers (seeded in migration 00004).
const (
	CatTransfer   = "transfer"
	CatCardPay    = "credit_card_payment"
	CatInvestment = "investments"
)

const (
	// DefaultWindowDays is how far apart the two sides may be dated (S12).
	DefaultWindowDays = 3
	// LearnedWindowDays applies to an account pair that has paired often before.
	LearnedWindowDays = 5
	// learnedAfter is how many earlier pairs make an account pair "known".
	learnedAfter = 3
	// askAfterDays is how long a lone transfer-looking transaction waits for a partner before it asks.
	askAfterDays = 3
)

// ErrInvalid means a bad request; ErrNotFound means no such transaction or pair.
var (
	ErrInvalid  = errors.New("transfers: invalid request")
	ErrNotFound = errors.New("transfers: not found")
)

// Service pairs and asks.
type Service struct {
	DB     *sql.DB
	Ledger *ledger.Service
	Now    func() time.Time
}

// New builds a Service.
func New(db *sql.DB, l *ledger.Service) *Service {
	return &Service{DB: db, Ledger: l, Now: time.Now}
}

type cand struct {
	id      string
	account string
	amount  int64
	date    civil.Date
	desc    string
	subtype string
}

// candidates are visible, settled, uncategorized transactions not yet in any pair or decision.
func (s *Service) candidates(ctx context.Context, q events.DBTX) ([]cand, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.id, r.account_id, r.amount, coalesce(r.transacted_on, r.posted_on), r.description, a.subtype
		FROM raw_transactions r
		JOIN accounts a ON a.id = r.account_id AND a.active
		LEFT JOIN transaction_overlays o ON o.raw_txn_id = r.id
		LEFT JOIN import_batches ib ON ib.id = r.import_batch_id
		WHERE NOT r.pending AND (ib.id IS NULL OR ib.status <> 'undone')
		  AND coalesce(o.hidden, false) = false AND coalesce(o.category_id, '') = '' AND o.reviewed_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM transfer_links t WHERE t.txn_a = r.id OR t.txn_b = r.id)
		ORDER BY r.posted_on, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cand
	for rows.Next() {
		var c cand
		var d time.Time
		if err = rows.Scan(&c.id, &c.account, &c.amount, &d, &c.desc, &c.subtype); err != nil {
			return nil, err
		}
		c.date = civil.DateOf(d)
		out = append(out, c)
	}
	return out, rows.Err()
}

// knownPairs counts earlier active pairs per unordered account pair.
func (s *Service) knownPairs(ctx context.Context, q events.DBTX) (map[[2]string]int, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT ra.account_id, rb.account_id, count(*) FROM transfer_links t
		JOIN raw_transactions ra ON ra.id = t.txn_a JOIN raw_transactions rb ON rb.id = t.txn_b
		WHERE t.kind = 'pair' AND t.state = 'active' GROUP BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string]int{}
	for rows.Next() {
		var a, b string
		var n int
		if err = rows.Scan(&a, &b, &n); err != nil {
			return nil, err
		}
		out[key(a, b)] += n
	}
	return out, rows.Err()
}

func key(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Pair finds opposite, equal, close-in-time rows in two different accounts and
// marks both as a neutral transfer. It is idempotent: a row already paired,
// decided or categorized is never touched, and a pair the user dissolved is
// never made again.
func (s *Service) Pair(ctx context.Context, tx *sql.Tx) (int, error) {
	cs, err := s.candidates(ctx, tx)
	if err != nil {
		return 0, err
	}
	known, err := s.knownPairs(ctx, tx)
	if err != nil {
		return 0, err
	}
	type edge struct {
		a, b int
		gap  int
	}
	var edges []edge
	for i := range cs {
		for j := i + 1; j < len(cs); j++ {
			x, y := cs[i], cs[j]
			if x.account == y.account || x.amount != -y.amount || x.amount == 0 {
				continue
			}
			window := DefaultWindowDays
			if known[key(x.account, y.account)] >= learnedAfter {
				window = LearnedWindowDays
			}
			gap := x.date.DaysSince(y.date)
			if gap < 0 {
				gap = -gap
			}
			if gap <= window {
				edges = append(edges, edge{i, j, gap})
			}
		}
	}
	sort.SliceStable(edges, func(p, q int) bool { return edges[p].gap < edges[q].gap })
	used := map[int]bool{}
	n := 0
	for _, e := range edges {
		if used[e.a] || used[e.b] {
			continue
		}
		used[e.a], used[e.b] = true, true
		if err = s.recordPair(ctx, tx, cs[e.a], cs[e.b]); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// pairCategory is the category both sides of a pair get (S12, S13).
func pairCategory(a, b cand) string {
	switch {
	case a.subtype == "investment" || b.subtype == "investment":
		return CatInvestment // counts toward savings
	case a.subtype == "credit_card" || b.subtype == "credit_card":
		return CatCardPay
	}
	return CatTransfer
}

func (s *Service) recordPair(ctx context.Context, tx *sql.Tx, a, b cand) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO transfer_links (txn_a, txn_b, kind, created_by) VALUES ($1,$2,'pair','auto')`, a.id, b.id); err != nil {
		return err
	}
	cat := pairCategory(a, b)
	for _, id := range []string{a.id, b.id} {
		c := cat
		if _, err := s.Ledger.Apply(ctx, tx, events.ActorSystem, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: &c}, ""); err != nil {
			return err
		}
	}
	return nil
}

// Unpair dissolves a pair the user says is not one. Both rows lose the
// transfer category (if it is still the one the pairing gave) and are never
// paired with each other again.
func (s *Service) Unpair(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID string) error {
	var id int64
	var a, b sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, txn_a, txn_b FROM transfer_links WHERE (txn_a=$1 OR txn_b=$1) AND kind='pair' AND state='active'`, txnID).Scan(&id, &a, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE transfer_links SET state='dissolved', created_by='user' WHERE id=$1`, id); err != nil {
		return err
	}
	for _, t := range []string{a.String, b.String} {
		cur, err := s.Ledger.Get(ctx, t)
		if err != nil {
			return err
		}
		if cur.CategoryID == CatTransfer || cur.CategoryID == CatCardPay || cur.CategoryID == CatInvestment {
			if _, err = s.Ledger.Apply(ctx, tx, actor, t, ledger.KindSetCategory, ledger.SetCategory{TxnID: t, CategoryID: nil}, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

var transferWords = regexp.MustCompile(`(?i)\b(transfer|xfer|trnsfr|to savings|from savings|to checking|from checking)\b`)

var investmentWords = regexp.MustCompile(`(?i)\b(vanguard|fidelity|schwab|robinhood|acorns|betterment|wealthfront|etrade|e\*trade|ira|401k|brokerage|invest)\b`)

// Ask is a one-sided transfer-looking transaction waiting for the user.
type Ask struct {
	Txn ledger.Txn
	// Default is the pre-selected choice: savings for an investment contribution, else neutral.
	Default string
	Options []string
}

// Asks lists lone transfer-looking transactions whose partner window has
// closed. A transaction in a linked-card payment or investment context is
// handled by Pair; everything else asks (S12). Nothing is decided without the user.
func (s *Service) Asks(ctx context.Context) ([]Ask, error) {
	cs, err := s.candidates(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	today := civil.DateOf(s.Now())
	var out []Ask
	for _, c := range cs {
		if !transferWords.MatchString(c.desc) && !investmentWords.MatchString(c.desc) {
			continue
		}
		if today.DaysSince(c.date) < askAfterDays {
			continue // its partner may still arrive
		}
		t, err := s.Ledger.Get(ctx, c.id)
		if err != nil {
			return nil, err
		}
		a := Ask{Txn: t, Default: "neutral", Options: []string{"neutral", "savings", "spending"}}
		if c.amount < 0 && (investmentWords.MatchString(c.desc) || c.subtype == "investment") {
			a.Default = "savings"
		}
		out = append(out, a)
	}
	return out, nil
}

// Resolve answers an ask: neutral, savings or spending. The choice is recorded
// so it is never asked again, and applied as an ordinary overlay event.
func (s *Service) Resolve(ctx context.Context, tx *sql.Tx, actor events.Actor, txnID, choice string) (events.Event, error) {
	var cat *string
	switch choice {
	case "neutral":
		c := CatTransfer
		cat = &c
	case "savings":
		c := CatInvestment
		cat = &c
	case "spending":
	default:
		return events.Event{}, fmt.Errorf("%w: choose neutral, savings or spending", ErrInvalid)
	}
	t, err := s.Ledger.Get(ctx, txnID)
	if err != nil {
		return events.Event{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO transfer_links (txn_a, kind, choice, created_by) VALUES ($1,'one_sided',$2,'user')
		ON CONFLICT (txn_a) DO UPDATE SET choice=EXCLUDED.choice, state='active'`, txnID, choice); err != nil {
		return events.Event{}, err
	}
	if cat == nil {
		if t.CategoryID != "" {
			return s.Ledger.Apply(ctx, tx, actor, txnID, ledger.KindSetCategory, ledger.SetCategory{TxnID: txnID, CategoryID: nil}, "")
		}
		return events.Event{}, nil
	}
	return s.Ledger.Apply(ctx, tx, actor, txnID, ledger.KindSetCategory, ledger.SetCategory{TxnID: txnID, CategoryID: cat}, "")
}

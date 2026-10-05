package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/money"
)

// Part is one part of a split transaction as seen in a view.
type Part struct {
	PartNo     int
	Amount     money.Cents
	CategoryID string
	Bucket     string
	Tags       []string
	Note       string
}

// Txn is an effective transaction: the bank's fields plus the user's overlay.
type Txn struct {
	ID            string
	AccountID     string
	ProviderTxnID string

	PostedOn          civil.Date
	PostedInstant     *time.Time
	TransactedOn      *civil.Date
	TransactedInstant *time.Time
	Amount            money.Cents // negative is an outflow
	Description       string
	Payee             string
	Memo              string
	MCC               string
	Pending           bool
	MissingFromBank   bool

	// overlay
	CategoryID        string
	MonthOverride     *civil.YearMonth
	Note              string
	Tags              []string
	Hidden            bool
	FromDiscretionary bool
	Type              string
	ReviewedAt        *time.Time

	// derived
	Month  civil.YearMonth // the override, else S3 on the transaction time, else the posted time
	Bucket string          // from the category; "" when uncategorized
	Splits []Part
}

// Filter narrows View.
type Filter struct {
	AccountID     string
	Month         *civil.YearMonth // resolved month, after overrides and zones
	IncludeHidden bool
	IDs           []string
	Cursor        string // from a previous page
	Limit         int    // 0 means no limit
}

// Page is one page of a view.
type Page struct {
	Items []Txn
	Next  string
}

const viewSelect = `
SELECT r.id, r.account_id, r.provider_txn_id, r.posted_on, r.posted_instant, r.transacted_on, r.transacted_instant,
       r.amount, r.description, r.payee, r.memo, r.mcc, r.pending, r.missing_from_bank_at IS NOT NULL,
       coalesce(o.category_id, ''), o.month_override, coalesce(o.note, ''), to_json(coalesce(o.tags, '{}'::text[])),
       coalesce(o.hidden, false), coalesce(o.from_discretionary, false), coalesce(o.type, ''), o.reviewed_at,
       coalesce(g.bucket, '')
FROM raw_transactions r
LEFT JOIN transaction_overlays o ON o.raw_txn_id = r.id
LEFT JOIN categories c ON c.id = o.category_id
LEFT JOIN category_groups g ON g.id = c.group_id`

// View returns effective transactions, newest first (posted date, then id).
func (s *Service) View(ctx context.Context, f Filter) (Page, error) {
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.AccountID != "" {
		where = append(where, "r.account_id = "+arg(f.AccountID))
	}
	if !f.IncludeHidden {
		where = append(where, "coalesce(o.hidden, false) = false")
	}
	if len(f.IDs) > 0 {
		where = append(where, "r.id = ANY("+arg(textArray(f.IDs))+"::uuid[])")
	}
	if f.Month != nil {
		// The resolved month can differ from the posted date by an override or a
		// zone conversion, so take a generous date window and settle it in Go.
		lo := f.Month.First().AddDays(-2)
		hi := f.Month.Last().AddDays(2)
		where = append(where, fmt.Sprintf("(o.month_override = %s OR coalesce(r.transacted_on, r.posted_on) BETWEEN %s AND %s)",
			arg(time.Date(f.Month.Y, f.Month.M, 1, 0, 0, 0, 0, time.UTC)), arg(toTime(lo)), arg(toTime(hi))))
	}
	if f.Cursor != "" {
		pos, err := httpx.DecodeCursor(f.Cursor)
		if err != nil {
			return Page{}, ErrInvalid
		}
		date, id, ok := strings.Cut(pos, "|")
		if !ok {
			return Page{}, ErrInvalid
		}
		d, err := civil.ParseDate(date)
		if err != nil {
			return Page{}, ErrInvalid
		}
		where = append(where, fmt.Sprintf("(r.posted_on, r.id) < (%s::date, %s::uuid)", arg(toTime(d)), arg(id)))
	}
	q := viewSelect
	if len(where) > 0 {
		q += "\nWHERE " + strings.Join(where, " AND ")
	}
	q += "\nORDER BY r.posted_on DESC, r.id DESC"
	// Month filtering happens after the query, so only limit in SQL when there is none.
	if f.Limit > 0 && f.Month == nil {
		q += fmt.Sprintf("\nLIMIT %d", f.Limit+1)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var out []Txn
	for rows.Next() {
		var t Txn
		var postedOn time.Time
		var transactedOn, monthOverride *time.Time
		var amount int64
		var tags []byte
		if err = rows.Scan(&t.ID, &t.AccountID, &t.ProviderTxnID, &postedOn, &t.PostedInstant, &transactedOn, &t.TransactedInstant,
			&amount, &t.Description, &t.Payee, &t.Memo, &t.MCC, &t.Pending, &t.MissingFromBank,
			&t.CategoryID, &monthOverride, &t.Note, &tags, &t.Hidden, &t.FromDiscretionary, &t.Type, &t.ReviewedAt, &t.Bucket); err != nil {
			return Page{}, err
		}
		t.Amount = money.Cents(amount)
		t.PostedOn = civil.DateOf(postedOn)
		if transactedOn != nil {
			d := civil.DateOf(*transactedOn)
			t.TransactedOn = &d
		}
		if monthOverride != nil {
			ym := civil.DateOf(*monthOverride).Month()
			t.MonthOverride = &ym
		}
		if err = json.Unmarshal(tags, &t.Tags); err != nil {
			return Page{}, err
		}
		t.Month = s.resolveMonth(t)
		if f.Month != nil && t.Month != *f.Month {
			continue
		}
		out = append(out, t)
	}
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	var next string
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = httpx.EncodeCursor(last.PostedOn.String() + "|" + last.ID)
	}
	if err = s.attachSplits(ctx, out); err != nil {
		return Page{}, err
	}
	return Page{Items: out, Next: next}, nil
}

func toTime(d civil.Date) time.Time { return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, time.UTC) }

// resolveMonth applies S3: a user override wins; otherwise the bank's
// transaction time (else posted time), where a date-only value stays a
// calendar date and a real instant converts to the home zone in force for the
// month it falls in.
func (s *Service) resolveMonth(t Txn) civil.YearMonth {
	if t.MonthOverride != nil {
		return *t.MonthOverride
	}
	tt := civil.TxnTime{}
	switch {
	case t.TransactedInstant != nil:
		tt.Instant = t.TransactedInstant
	case t.TransactedOn != nil:
		tt.Date = t.TransactedOn
	case t.PostedInstant != nil:
		tt.Instant = t.PostedInstant
	default:
		d := t.PostedOn
		tt.Date = &d
	}
	if tt.Instant == nil {
		return civil.MonthOf(tt, nil)
	}
	// the zone depends on the month, which depends on the zone: use the UTC month as the key
	zone := civil.HomeZoneAt(civil.MonthOf(tt, time.UTC), s.Zones)
	return civil.MonthOf(tt, zone)
}

func (s *Service) attachSplits(ctx context.Context, txns []Txn) error {
	if len(txns) == 0 {
		return nil
	}
	ids := make([]string, len(txns))
	index := map[string]int{}
	for i, t := range txns {
		ids[i] = t.ID
		index[t.ID] = i
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT sp.raw_txn_id, sp.part_no, sp.amount, sp.category_id, g.bucket, to_json(sp.tags), sp.note
		FROM txn_splits sp JOIN categories c ON c.id = sp.category_id JOIN category_groups g ON g.id = c.group_id
		WHERE sp.raw_txn_id = ANY($1::uuid[]) ORDER BY sp.raw_txn_id, sp.part_no`, textArray(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p Part
		var amount int64
		var tags []byte
		if err = rows.Scan(&id, &p.PartNo, &amount, &p.CategoryID, &p.Bucket, &tags, &p.Note); err != nil {
			return err
		}
		p.Amount = money.Cents(amount)
		if err = json.Unmarshal(tags, &p.Tags); err != nil {
			return err
		}
		txns[index[id]].Splits = append(txns[index[id]].Splits, p)
	}
	return rows.Err()
}

// Get returns one transaction (hidden ones included), or ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Txn, error) {
	p, err := s.View(ctx, Filter{IDs: []string{id}, IncludeHidden: true})
	if err != nil {
		return Txn{}, err
	}
	if len(p.Items) == 0 {
		return Txn{}, ErrNotFound
	}
	return p.Items[0], nil
}

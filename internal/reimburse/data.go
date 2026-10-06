package reimburse

import (
	"context"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/normalize"
)

// row is a transaction as the matcher needs it.
type row struct {
	ID          string
	Account     string
	Currency    string
	Amount      int64
	Date        civil.Date // transaction date when provided, else posted (S12a dating)
	Posted      civil.Date
	Description string
	Payee       string
	Memo        string
	Category    string
	Bucket      string
	Reviewed    bool
	Pending     bool
	Linked      int64 // cents already netted by active links
}

const rowSelect = `
SELECT r.id, r.account_id, a.currency, r.amount, coalesce(r.transacted_on, r.posted_on), r.posted_on, r.description, r.payee, r.memo,
       coalesce(o.category_id, ''), coalesce(g.bucket, ''), o.reviewed_at IS NOT NULL, r.pending,
       coalesce((SELECT sum(l.amount) FROM links l WHERE (l.payment_txn_id = r.id OR l.expense_txn_id = r.id) AND l.state IN ('tentative','confirmed')), 0)
FROM raw_transactions r
JOIN accounts a ON a.id = r.account_id AND a.active
LEFT JOIN transaction_overlays o ON o.raw_txn_id = r.id
LEFT JOIN categories c ON c.id = o.category_id
LEFT JOIN category_groups g ON g.id = c.group_id
LEFT JOIN import_batches ib ON ib.id = r.import_batch_id
WHERE (ib.id IS NULL OR ib.status <> 'undone') AND coalesce(o.hidden, false) = false AND r.missing_from_bank_at IS NULL`

func scanRow(rs interface{ Scan(...any) error }) (row, error) {
	var x row
	var d, p time.Time
	if err := rs.Scan(&x.ID, &x.Account, &x.Currency, &x.Amount, &d, &p, &x.Description, &x.Payee, &x.Memo, &x.Category, &x.Bucket, &x.Reviewed, &x.Pending, &x.Linked); err != nil {
		return x, err
	}
	x.Date, x.Posted = civil.DateOf(d), civil.DateOf(p)
	return x, nil
}

func queryRows(ctx context.Context, q events.DBTX, where string, args ...any) ([]row, error) {
	rs, err := q.QueryContext(ctx, rowSelect+where+` ORDER BY r.posted_on, r.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		x, err := scanRow(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rs.Err()
}

// payer is the text a payer is recognized from: description and payee, never the memo.
func (x row) payerText() string { return x.Description + " " + x.Payee }

func (x row) memoText() string { return strings.ToLower(x.Memo + " " + x.Description) }

// merchant is the normalized key of an expense.
func (x row) merchant() string { return normalize.MerchantKey(x.Description) }

// isP2P reports whether a deposit reads like a person paying the user: a
// P2P platform, a person-to-person bank transfer, or a recognized payer.
func isP2P(x row, known bool) bool {
	if known {
		return true
	}
	switch counterparties.PlatformOf(x.Description) {
	case counterparties.PlatformZelle, counterparties.PlatformVenmo, counterparties.PlatformCashApp, counterparties.PlatformPayPal:
		return true
	case counterparties.PlatformBank:
		return !counterparties.ParseName(x.Description).Empty() // "Transfer from <NAME>"
	}
	return false
}

// eligiblePayment: money in, visible, not already settled by links, not a
// transfer or income the user decided, and not labelled as something else.
func eligiblePayment(x row) bool {
	if x.Amount <= 0 || x.Linked >= x.Amount {
		return false
	}
	switch x.Bucket {
	case "neutral", "income", "savings":
		return x.Category == "reimbursement" || x.Category == "refund"
	}
	return true
}

func eligibleExpense(x row) bool {
	if x.Amount >= 0 || -x.Amount <= x.Linked {
		return false
	}
	return x.Bucket != "neutral" && x.Bucket != "income" && x.Bucket != "savings"
}

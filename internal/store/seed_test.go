//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/alec-jensen/sofar/internal/testkit"
)

// The stable ids the legacy importer maps to (docs/plan/legacy-migration.md) and
// the bucket each must land in.
var stableIDs = map[string]string{
	"rent_mortgage": "bills", "utilities": "bills", "internet_phone": "bills", "insurance": "bills", "loan_payments": "bills", "other_bills": "bills",
	"other_subscriptions": "subscriptions", "memberships": "subscriptions", "streaming_media": "subscriptions", "software_cloud": "subscriptions",
	"education": "spending", "groceries": "spending", "dining_out": "spending", "fuel": "spending", "auto_transit": "spending",
	"shopping": "spending", "home_household": "spending", "entertainment": "spending", "health_medical": "spending",
	"personal_care": "spending", "travel": "spending", "gifts_donations": "spending", "fees_interest": "spending",
	"business_expenses": "spending", "cash_atm": "spending", "other_spending": "spending",
	"general_savings": "savings", "emergency_fund": "savings", "investments": "savings", "debt_payoff": "savings",
	"salary": "income", "self_employment": "income", "interest": "income", "gift_other": "income",
	"rewards_cashback": "income", "one_time_income": "income", "asset_sale": "income",
	"transfer": "neutral", "credit_card_payment": "neutral", "loan_proceeds": "neutral",
	"reimbursement": "neutral", "refund": "neutral", "excluded": "neutral",
}

func TestS1a_DefaultsSeededWithStableIDs(t *testing.T) {
	db := testkit.DB(t)
	rows, err := db.QueryContext(context.Background(), `SELECT c.id, g.bucket FROM categories c JOIN category_groups g ON g.id = c.group_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, bucket string
		rows.Scan(&id, &bucket)
		got[id] = bucket
	}
	for id, bucket := range stableIDs {
		if got[id] != bucket {
			t.Errorf("category %q: bucket %q, want %q", id, got[id], bucket)
		}
	}
	if len(got) != len(stableIDs) {
		t.Errorf("seeded %d categories, expected %d", len(got), len(stableIDs))
	}
}

func TestS1a_EveryCategoryHasGroupAndBucket(t *testing.T) {
	db := testkit.DB(t)
	ctx := context.Background()
	var orphans int
	db.QueryRowContext(ctx, `SELECT count(*) FROM categories c LEFT JOIN category_groups g ON g.id = c.group_id WHERE g.id IS NULL`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d categories without a group", orphans)
	}
	var bad int
	db.QueryRowContext(ctx, `SELECT count(*) FROM category_groups WHERE bucket NOT IN ('income','bills','subscriptions','savings','spending','neutral')`).Scan(&bad)
	if bad != 0 {
		t.Errorf("%d groups outside the fixed buckets", bad)
	}
	// every non-neutral bucket has an "other" fallback for deletions (S1a)
	for bucket, fallback := range map[string]string{"income": "gift_other", "bills": "other_bills", "subscriptions": "other_subscriptions", "savings": "general_savings", "spending": "other_spending"} {
		var n int
		db.QueryRowContext(ctx, `SELECT count(*) FROM categories c JOIN category_groups g ON g.id=c.group_id WHERE c.id=$1 AND g.bucket=$2`, fallback, bucket).Scan(&n)
		if n != 1 {
			t.Errorf("bucket %s lacks its fallback category %s", bucket, fallback)
		}
	}
	// a database constraint refuses an unknown bucket
	if _, err := db.ExecContext(ctx, `INSERT INTO category_groups (id, name, bucket) VALUES ('x','x','mystery')`); err == nil {
		t.Error("an unknown bucket must be refused")
	}
}

func TestLedger_RawTransactionUniquePerAccount(t *testing.T) {
	db := testkit.DB(t)
	ctx := context.Background()
	var acct string
	if err := db.QueryRowContext(ctx, `INSERT INTO accounts (provider, provider_account_id, name, kind) VALUES ('manual','a1','checking','budget') RETURNING id`).Scan(&acct); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO raw_transactions (account_id, provider_txn_id, posted_on, amount) VALUES ($1,'t1','2026-10-01',-500)`
	if _, err := db.ExecContext(ctx, ins, acct); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins, acct); err == nil {
		t.Error("the same provider id twice in one account must be refused")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (provider, provider_account_id, name, kind) VALUES ('manual','a1','dupe','budget')`); err == nil {
		t.Error("duplicate (provider, connection, provider account) must be refused")
	}
}

//go:build integration

package ledger_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/testkit"
)

func setup(t *testing.T) (*ledger.Service, *sql.DB, string) {
	t.Helper()
	db := testkit.DB(t)
	svc := ledger.New(db, events.NewRegistry())
	return svc, db, testkit.Account(t, db, "checking")
}

func apply(t testing.TB, svc *ledger.Service, db *sql.DB, txn, kind string, payload any) events.Event {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	e, err := svc.Apply(context.Background(), tx, events.ActorUser, txn, kind, payload, "")
	if err != nil {
		t.Fatalf("apply %s: %v", kind, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return e
}

func str(s string) *string { return &s }

func TestS11_OverlayNeverTouchesBankFields(t *testing.T) {
	svc, db, acct := setup(t)
	id := testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -1234, Description: "SQ *COFFEE #12", Payee: "Coffee Co", PostedOn: "2026-10-03"})
	before := rawRow(t, db, id)
	apply(t, svc, db, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: str("dining_out")})
	apply(t, svc, db, id, ledger.KindSetNote, ledger.SetNote{TxnID: id, Note: "team lunch"})
	apply(t, svc, db, id, ledger.KindSetTags, ledger.SetTags{TxnID: id, Tags: []string{"work", "q4"}})
	apply(t, svc, db, id, ledger.KindSetMonth, ledger.SetMonth{TxnID: id, Month: str("2026-11")})
	apply(t, svc, db, id, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: true})
	apply(t, svc, db, id, ledger.KindSetFromDiscretionary, ledger.SetFromDiscretionary{TxnID: id, Value: true})
	if after := rawRow(t, db, id); after != before {
		t.Fatalf("bank fields changed:\n before %q\n after  %q", before, after)
	}
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.CategoryID != "dining_out" || got.Bucket != "spending" || got.Note != "team lunch" || !reflect.DeepEqual(got.Tags, []string{"work", "q4"}) ||
		got.Month != (civil.YearMonth{Y: 2026, M: 11}) || got.ReviewedAt == nil || !got.FromDiscretionary || got.Description != "SQ *COFFEE #12" {
		t.Fatalf("unexpected view: %+v", got)
	}
}

func rawRow(t testing.TB, db *sql.DB, id string) string {
	t.Helper()
	var s string
	err := db.QueryRow(`SELECT concat_ws('|', posted_on, amount, description, payee, memo, mcc, pending, fingerprint, provider_txn_id) FROM raw_transactions WHERE id=$1`, id).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestS11_HiddenExcludedAndTombstoned(t *testing.T) {
	svc, db, acct := setup(t)
	id := testkit.RawTxn(t, db, acct, testkit.Raw{ProviderID: "bank-1", Amount: -500})
	keep := testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -700})
	ctx := context.Background()
	hide := apply(t, svc, db, id, ledger.KindHide, ledger.Hide{TxnID: id, Reason: "duplicate"})
	page, _ := svc.View(ctx, ledger.Filter{})
	if len(page.Items) != 1 || page.Items[0].ID != keep {
		t.Fatalf("hidden rows must be excluded: %+v", page.Items)
	}
	page, _ = svc.View(ctx, ledger.Filter{IncludeHidden: true})
	if len(page.Items) != 2 {
		t.Fatalf("IncludeHidden should return both: %d", len(page.Items))
	}
	tombstones := func() int {
		var n int
		db.QueryRow(`SELECT count(*) FROM hidden_tombstones WHERE account_id=$1 AND provider_txn_id='bank-1'`, acct).Scan(&n)
		return n
	}
	if tombstones() != 1 {
		t.Fatal("hiding must write a tombstone")
	}
	tx, _ := db.Begin()
	if _, err := svc.Undo(ctx, tx, events.ActorUser, hide.ID, ""); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if tombstones() != 0 {
		t.Fatal("unhiding must remove the tombstone")
	}
	if page, _ = svc.View(ctx, ledger.Filter{}); len(page.Items) != 2 {
		t.Fatal("an undone hide shows the row again")
	}
	// explicit unhide event does the same
	apply(t, svc, db, id, ledger.KindHide, ledger.Hide{TxnID: id})
	apply(t, svc, db, id, ledger.KindUnhide, ledger.Unhide{TxnID: id})
	if tombstones() != 0 {
		t.Fatal("unhide event must remove the tombstone")
	}
}

func TestS19_6_SplitPartsSumToTotal(t *testing.T) {
	svc, db, acct := setup(t)
	id := testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -10000})
	ctx := context.Background()
	tx, _ := db.Begin()
	defer tx.Rollback()
	_, err := svc.Apply(ctx, tx, events.ActorUser, id, ledger.KindSplit, ledger.Split{TxnID: id, Parts: []ledger.SplitPart{{Amount: -6000, CategoryID: "groceries"}, {Amount: -3000, CategoryID: "home_household"}}}, "")
	if !errors.Is(err, ledger.ErrSplitSum) {
		t.Fatalf("parts that do not add up must be refused, got %v", err)
	}
	if _, err = svc.Apply(ctx, tx, events.ActorUser, id, ledger.KindSplit, ledger.Split{TxnID: id, Parts: []ledger.SplitPart{{Amount: -10000, CategoryID: "groceries"}}}, ""); !errors.Is(err, ledger.ErrInvalid) {
		t.Fatalf("a one-part split is not a split, got %v", err)
	}
	if _, err = svc.Apply(ctx, tx, events.ActorUser, id, ledger.KindSplit, ledger.Split{TxnID: id, Parts: []ledger.SplitPart{{Amount: -6000, CategoryID: "groceries"}, {Amount: -4000, CategoryID: "home_household", Tags: []string{"house"}, Note: "soap"}}}, ""); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	got, _ := svc.Get(ctx, id)
	if len(got.Splits) != 2 || got.Splits[1].Note != "soap" || got.Splits[1].Tags[0] != "house" || got.Splits[0].Bucket != "spending" {
		t.Fatalf("splits: %+v", got.Splits)
	}
}

func TestA9_Inv9_SplitCountsInOneBucketPerPart(t *testing.T) {
	svc, db, acct := setup(t)
	id := testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -20000})
	apply(t, svc, db, id, ledger.KindSplit, ledger.Split{TxnID: id, Parts: []ledger.SplitPart{{Amount: -15000, CategoryID: "rent_mortgage"}, {Amount: -5000, CategoryID: "groceries"}}})
	got, _ := svc.Get(context.Background(), id)
	byBucket := map[string]int64{}
	var total int64
	for _, p := range got.Splits {
		byBucket[p.Bucket] += int64(p.Amount)
		total += int64(p.Amount)
	}
	if byBucket["bills"] != -15000 || byBucket["spending"] != -5000 || total != int64(got.Amount) {
		t.Fatalf("each part must land in exactly one bucket and sum to the total: %v total=%d", byBucket, total)
	}
}

func TestS3_MonthResolution(t *testing.T) {
	svc, db, acct := setup(t)
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("no tzdata")
	}
	svc.Zones = []civil.ZoneChange{{From: civil.YearMonth{Y: 2020, M: 1}, Zone: chicago}}
	lateInstant := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC) // Oct 31 23:30 in Chicago
	dateOnly := testkit.RawTxn(t, db, acct, testkit.Raw{PostedOn: "2026-10-31", TransactedOn: "2026-11-01", Amount: -100})
	instant := testkit.RawTxn(t, db, acct, testkit.Raw{PostedOn: "2026-11-01", TransactedInstant: &lateInstant, Amount: -100})
	postedOnly := testkit.RawTxn(t, db, acct, testkit.Raw{PostedOn: "2026-09-30", Amount: -100})
	for id, want := range map[string]civil.YearMonth{dateOnly: {Y: 2026, M: 11}, instant: {Y: 2026, M: 10}, postedOnly: {Y: 2026, M: 9}} {
		got, err := svc.Get(context.Background(), id)
		if err != nil || got.Month != want {
			t.Errorf("month = %v (%v), want %v", got.Month, err, want)
		}
	}
	nov := civil.YearMonth{Y: 2026, M: 11}
	page, _ := svc.View(context.Background(), ledger.Filter{Month: &nov})
	if len(page.Items) != 1 || page.Items[0].ID != dateOnly {
		t.Fatalf("month filter uses the resolved month: %+v", page.Items)
	}
	apply(t, svc, db, postedOnly, ledger.KindSetMonth, ledger.SetMonth{TxnID: postedOnly, Month: str("2026-11")})
	if page, _ = svc.View(context.Background(), ledger.Filter{Month: &nov}); len(page.Items) != 2 {
		t.Fatalf("an override moves the row into November: %d", len(page.Items))
	}
	tx, _ := db.Begin()
	if _, err = svc.Apply(context.Background(), tx, events.ActorUser, postedOnly, ledger.KindSetMonth, ledger.SetMonth{TxnID: postedOnly, Month: str("nonsense")}, ""); !errors.Is(err, ledger.ErrInvalid) {
		t.Errorf("a bad month must be refused: %v", err)
	}
	tx.Rollback()
}

func TestLedger_ViewPagination(t *testing.T) {
	svc, db, acct := setup(t)
	for i := 1; i <= 7; i++ {
		testkit.RawTxn(t, db, acct, testkit.Raw{PostedOn: fmt.Sprintf("2026-10-%02d", i), Amount: -int64(i)})
	}
	var seen []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		p, err := svc.View(context.Background(), ledger.Filter{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			seen = append(seen, it.PostedOn.String())
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
	}
	want := []string{"2026-10-07", "2026-10-06", "2026-10-05", "2026-10-04", "2026-10-03", "2026-10-02", "2026-10-01"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("pages = %v, want %v", seen, want)
	}
	if _, err := svc.View(context.Background(), ledger.Filter{Cursor: "!!!"}); !errors.Is(err, ledger.ErrInvalid) {
		t.Errorf("bad cursor: %v", err)
	}
}

func TestLedger_UndoRestoresOverlay(t *testing.T) {
	svc, db, acct := setup(t)
	id := testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -900})
	e1 := apply(t, svc, db, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: str("groceries")})
	apply(t, svc, db, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: str("dining_out")})
	cat := func() string { g, _ := svc.Get(context.Background(), id); return g.CategoryID }
	if cat() != "dining_out" {
		t.Fatal("latest wins")
	}
	ctx := context.Background()
	tx, _ := db.Begin()
	// undoing the older event leaves the newer one in force
	u, err := svc.Undo(ctx, tx, events.ActorUser, e1.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if cat() != "dining_out" {
		t.Fatal("undoing an older event must not change the newer result")
	}
	tx, _ = db.Begin()
	if _, err = svc.Redo(ctx, tx, events.ActorUser, u.ID, ""); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if cat() != "dining_out" {
		t.Fatal("redo")
	}
}

// TestLedger_ProjectionRebuildEqualsIncremental applies random overlay events and
// undos, then checks that rebuilding every overlay from events gives the same rows.
func TestLedger_ProjectionRebuildEqualsIncremental(t *testing.T) {
	svc, db, acct := setup(t)
	ctx := context.Background()
	txns := []string{
		testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -1000}),
		testkit.RawTxn(t, db, acct, testkit.Raw{Amount: -2500}),
		testkit.RawTxn(t, db, acct, testkit.Raw{Amount: 4000}),
	}
	cats := []string{"groceries", "dining_out", "salary", "transfer"}
	snapshot := func(q interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}) string {
		var out string
		for _, query := range []string{
			`SELECT concat_ws('|', raw_txn_id, category_id, month_override, note, tags, hidden, from_discretionary, coalesce(type,''), reviewed_at IS NOT NULL) FROM transaction_overlays ORDER BY raw_txn_id`,
			`SELECT concat_ws('|', raw_txn_id, part_no, amount, category_id, note) FROM txn_splits ORDER BY raw_txn_id, part_no`,
			`SELECT concat_ws('|', provider_txn_id) FROM hidden_tombstones ORDER BY provider_txn_id`,
		} {
			rows, err := q.QueryContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var s string
				rows.Scan(&s)
				out += s + "\n"
			}
			rows.Close()
			out += "--\n"
		}
		return out
	}
	rapid.Check(t, func(rt *rapid.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			rt.Fatal(err)
		}
		defer tx.Rollback()
		var applied []int64
		for i, n := 0, rapid.IntRange(1, 15).Draw(rt, "n"); i < n; i++ {
			id := rapid.SampledFrom(txns).Draw(rt, "txn")
			var kind string
			var payload any
			switch rapid.IntRange(0, 6).Draw(rt, "kind") {
			case 0:
				kind, payload = ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: str(rapid.SampledFrom(cats).Draw(rt, "cat"))}
			case 1:
				kind, payload = ledger.KindSetNote, ledger.SetNote{TxnID: id, Note: rapid.StringMatching(`[a-z ]{0,8}`).Draw(rt, "note")}
			case 2:
				kind, payload = ledger.KindHide, ledger.Hide{TxnID: id}
			case 3:
				kind, payload = ledger.KindUnhide, ledger.Unhide{TxnID: id}
			case 4:
				kind, payload = ledger.KindSetMonth, ledger.SetMonth{TxnID: id, Month: str("2026-1" + fmt.Sprint(rapid.IntRange(0, 2).Draw(rt, "m")))}
			case 5:
				kind, payload = ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: rapid.Bool().Draw(rt, "r")}
			case 6: // undo something
				if len(applied) == 0 {
					continue
				}
				target := rapid.SampledFrom(applied).Draw(rt, "target")
				if _, err := svc.Undo(ctx, tx, events.ActorUser, target, ""); err != nil && !errors.Is(err, events.ErrAlreadyUndone) {
					rt.Fatal(err)
				}
				continue
			}
			e, err := svc.Apply(ctx, tx, events.ActorUser, id, kind, payload, "")
			if err != nil {
				rt.Fatal(err)
			}
			applied = append(applied, e.ID)
		}
		incremental := snapshot(tx)
		if err = svc.RebuildAll(ctx, tx); err != nil {
			rt.Fatal(err)
		}
		if rebuilt := snapshot(tx); rebuilt != incremental {
			rt.Fatalf("rebuild differs from incremental:\n--- incremental\n%s\n--- rebuilt\n%s", incremental, rebuilt)
		}
	})
}

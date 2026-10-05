//go:build integration

package importer_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/importer"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/testkit"
)

func mustDate(s string) civil.Date {
	d, err := civil.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

const csvFile = "Date,Description,Amount\n" +
	"2026-10-01,COFFEE,-4.50\n" +
	"2026-10-01,COFFEE,-4.50\n" + // a second identical purchase the same day is a real transaction
	"2026-10-02,PAYROLL,1500.00\n"

type env struct {
	db   *sql.DB
	imp  *importer.Service
	led  *ledger.Service
	acct string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	return &env{db: db, imp: importer.New(db, reg), led: ledger.New(db, reg), acct: testkit.Account(t, db, "checking")}
}

func parse(t *testing.T, s string) importer.Parsed {
	t.Helper()
	p, err := importer.ParseCSV(strings.NewReader(s), importer.Mapping{HasHeader: true, Date: "Date", Description: "Description", Amount: "Amount"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func visible(t *testing.T, e *env) int {
	t.Helper()
	p, err := e.led.View(context.Background(), ledger.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(p.Items)
}

func TestS19_5_CSVDryRunWritesNothing(t *testing.T) {
	e := setup(t)
	b, err := e.imp.DryRun(context.Background(), e.acct, "csv", "oct.csv", parse(t, csvFile))
	if err != nil {
		t.Fatal(err)
	}
	if b.Summary.Total != 3 || b.Summary.Add != 3 || b.Summary.Duplicates != 0 || b.Summary.From != "2026-10-01" || b.Summary.To != "2026-10-02" {
		t.Fatalf("summary: %+v", b.Summary)
	}
	for _, table := range []string{"raw_transactions", "transaction_overlays", "events", "hidden_tombstones"} {
		if n := count(t, e.db, table); n != 0 {
			t.Errorf("a dry run must not write %s, found %d rows", table, n)
		}
	}
	if n := count(t, e.db, "import_batches"); n != 1 {
		t.Errorf("the staged batch is recorded, found %d", n)
	}
}

func TestS19_5_ImportApplyAndBatchUndo(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	b, _ := e.imp.DryRun(ctx, e.acct, "csv", "oct.csv", parse(t, csvFile))
	ev, sum, err := e.imp.Apply(ctx, b.ID, events.ActorUser)
	if err != nil || sum.Add != 3 {
		t.Fatalf("apply: %+v %v", sum, err)
	}
	if visible(t, e) != 3 || count(t, e.db, "raw_transactions") != 3 {
		t.Fatalf("want 3 visible rows, got %d", visible(t, e))
	}
	if _, _, err = e.imp.Apply(ctx, b.ID, events.ActorUser); !errors.Is(err, importer.ErrBatchState) {
		t.Fatalf("a batch cannot be applied twice: %v", err)
	}

	undo, err := e.imp.Undo(ctx, events.ActorUser, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if visible(t, e) != 0 {
		t.Fatal("undo hides the batch's rows")
	}
	if count(t, e.db, "raw_transactions") != 3 {
		t.Fatal("undo must never delete rows")
	}
	if _, err = e.imp.Redo(ctx, events.ActorUser, undo.ID); err != nil {
		t.Fatal(err)
	}
	if visible(t, e) != 3 {
		t.Fatal("redo brings the rows back")
	}
}

func TestA9_Inv11_ImportTwiceIdentical(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	snapshot := func() string {
		rows, err := e.db.Query(`SELECT concat_ws('|', provider_txn_id, posted_on, amount, description, fingerprint) FROM raw_transactions ORDER BY provider_txn_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			rows.Scan(&s)
			out = append(out, s)
		}
		return strings.Join(out, "\n")
	}
	b1, _ := e.imp.DryRun(ctx, e.acct, "csv", "a.csv", parse(t, csvFile))
	e.imp.Apply(ctx, b1.ID, events.ActorUser)
	first := snapshot()

	b2, err := e.imp.DryRun(ctx, e.acct, "csv", "a-again.csv", parse(t, csvFile))
	if err != nil {
		t.Fatal(err)
	}
	if b2.Summary.Add != 0 || b2.Summary.Duplicates != 3 {
		t.Fatalf("the second dry run should find every row a duplicate: %+v", b2.Summary)
	}
	if _, _, err = e.imp.Apply(ctx, b2.ID, events.ActorUser); err != nil {
		t.Fatal(err)
	}
	if snapshot() != first {
		t.Fatal("importing the same file twice must leave the ledger identical")
	}
}

func TestS19_5_DedupeAgainstOtherSourcesByFingerprint(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// the bank feed already delivered the coffee once, under its own id
	fp := importer.Fingerprint(e.acct, mustDate("2026-10-01"), -450, "COFFEE")
	testkit.RawTxn(t, e.db, e.acct, testkit.Raw{ProviderID: "simplefin-1", PostedOn: "2026-10-01", Amount: -450, Description: "coffee"})
	e.db.Exec(`UPDATE raw_transactions SET fingerprint=$1`, fp)
	b, _ := e.imp.DryRun(ctx, e.acct, "csv", "oct.csv", parse(t, csvFile))
	// one of the two identical coffees is the feed's; the second is a real extra purchase
	if b.Summary.Duplicates != 1 || b.Summary.Add != 2 {
		t.Fatalf("summary: %+v", b.Summary)
	}
}

func TestS19_5_HiddenTransactionsNeverReimported(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	b, _ := e.imp.DryRun(ctx, e.acct, "csv", "oct.csv", parse(t, csvFile))
	e.imp.Apply(ctx, b.ID, events.ActorUser)
	p, _ := e.led.View(ctx, ledger.Filter{})
	victim := p.Items[0]
	tx, _ := e.db.Begin()
	if _, err := e.led.Apply(ctx, tx, events.ActorUser, victim.ID, ledger.KindHide, ledger.Hide{TxnID: victim.ID}, ""); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	b2, _ := e.imp.DryRun(ctx, e.acct, "csv", "again.csv", parse(t, csvFile))
	if b2.Summary.Hidden != 1 || b2.Summary.Add != 0 {
		t.Fatalf("a hidden row is reported and not re-added: %+v", b2.Summary)
	}
}

func TestS19_5_ReimportAfterUndoRevivesRows(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	b, _ := e.imp.DryRun(ctx, e.acct, "csv", "oct.csv", parse(t, csvFile))
	ev, _, _ := e.imp.Apply(ctx, b.ID, events.ActorUser)
	e.imp.Undo(ctx, events.ActorUser, ev.ID)
	b2, _ := e.imp.DryRun(ctx, e.acct, "csv", "oct.csv", parse(t, csvFile))
	if b2.Summary.Revive != 3 || b2.Summary.Add != 0 {
		t.Fatalf("rows from an undone batch come back on re-import: %+v", b2.Summary)
	}
	if _, _, err := e.imp.Apply(ctx, b2.ID, events.ActorUser); err != nil {
		t.Fatal(err)
	}
	if visible(t, e) != 3 || count(t, e.db, "raw_transactions") != 3 {
		t.Fatalf("visible=%d raw=%d", visible(t, e), count(t, e.db, "raw_transactions"))
	}
}

func TestS19_5_OFXImportUsesFITIDForDedupe(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	src := `<OFX><BANKTRANLIST>
<STMTTRN><DTPOSTED>20261001<TRNAMT>-4.50<FITID>F1<NAME>COFFEE</STMTTRN>
<STMTTRN><DTPOSTED>20261001<TRNAMT>-4.50<FITID>F2<NAME>COFFEE</STMTTRN>
</BANKTRANLIST></OFX>`
	p, err := importer.ParseOFX(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := e.imp.DryRun(ctx, e.acct, "ofx", "x.ofx", p)
	if _, _, err = e.imp.Apply(ctx, b.ID, events.ActorUser); err != nil {
		t.Fatal(err)
	}
	b2, _ := e.imp.DryRun(ctx, e.acct, "ofx", "x.ofx", p)
	if b2.Summary.Duplicates != 2 || b2.Summary.Add != 0 {
		t.Fatalf("%+v", b2.Summary)
	}
	if visible(t, e) != 2 {
		t.Fatalf("two distinct FITIDs are two transactions, even when identical otherwise")
	}
}

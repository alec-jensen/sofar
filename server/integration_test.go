//go:build integration

package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/alec-jensen/sofar/internal/db"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/pquerna/otp/totp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFullFlow(t *testing.T) {
	dbURL := os.Getenv("SOFAR_TEST_DATABASE_URL")
	if dbURL == "" {
		root, e := os.Getwd()
		if e != nil {
			t.Fatal(e)
		}
		cache := filepath.Join(root, ".cache")
		if e = os.MkdirAll(cache, 0700); e != nil {
			t.Fatal(e)
		}
		runtime, e := os.MkdirTemp(cache, "pg-test-")
		if e != nil {
			t.Fatal(e)
		}
		pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().Version(embeddedpostgres.V17).Port(55439).Database("sofar_test").Username("sofar_test").Password("local-test-only").CachePath(cache).BinariesPath(filepath.Join(cache, "binaries")).RuntimePath(runtime).DataPath(filepath.Join(runtime, "data")).BinaryRepositoryURL("https://repo.maven.apache.org/maven2").StartTimeout(60 * time.Second))
		if e = pg.Start(); e != nil {
			t.Fatal(e)
		}
		defer pg.Stop()
		dbURL = "postgres://sofar_test:local-test-only@localhost:55439/sofar_test?sslmode=disable"
	}
	database, e := db.Open(context.Background(), dbURL)
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	block, _ := aes.NewCipher(make([]byte, 32))
	aead, _ := cipher.NewGCM(block)
	if e = seedDefaults(context.Background(), database); e != nil {
		t.Fatal(e)
	}
	s := &server{db: database, encryption: aead, origin: "http://sofar.test", client: &http.Client{Timeout: 10 * time.Second}, bridgeHosts: []string{"127.0.0.1"}, attempts: map[string]attempt{}}
	handler := s.routes()
	var cookie *http.Cookie
	call := func(path string, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		method := "POST"
		if body == "" {
			method = "GET"
		}
		r := httptest.NewRequest(method, "http://sofar.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: status %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	call("/api/state", "", 401)
	t.Setenv("SOFAR_SETUP_KEY", "this-is-a-test-setup-key-with-enough-length")
	response := call("/api/auth/setup", `{"username":"test","password":"a long test password","setupKey":"this-is-a-test-setup-key-with-enough-length"}`, 200)
	cookie = response.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	call("/api/state", "", 200)
	if _, e = database.Exec(`INSERT INTO accounts(id,connection_id,external_id,institution_name,account_type,account_subtype,display_name) VALUES('a','conn1','acct-a','Test Bank','depository','checking','Checking'),('b','conn2','acct-b','Other Bank','depository','savings','Savings'); INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction,category_id,review_status) VALUES('expense','a','2026-08-01',10000,'Dinner','dinner','out','spending','confirmed'),('credit','b','2026-08-03',4000,'Venmo','venmo','in',NULL,'pending');`); e != nil {
		t.Fatal(e)
	}
	repayment := `{"id":"offline-action-001","type":"reimburse","expenseId":"expense","creditId":"credit","amount":4000}`
	call("/api/actions", repayment, 200)
	call("/api/actions", repayment, 200)
	var links int
	if e = database.QueryRow("SELECT count(*) FROM reimbursement_links").Scan(&links); e != nil || links != 1 {
		t.Fatalf("offline retry duplicated netting: %d %v", links, e)
	}
	call("/api/actions", `{"id":"offline-action-002","type":"reimburse","expenseId":"expense","creditId":"credit","amount":4001}`, 400)
	call("/api/actions", `{"id":"offline-action-003","type":"unlink","linkId":"offline-action-001"}`, 200)
	var status string
	if e = database.QueryRow("SELECT review_status FROM transactions WHERE id='credit'").Scan(&status); e != nil || status != "pending" {
		t.Fatal("unlink must restore credit for classification", e)
	}
	call("/api/actions", `{"id":"offline-action-004","type":"review","transactionId":"credit","category":"spending","incomeStream":"self-employed"}`, 200)
	call("/api/actions", `{"id":"offline-action-005","type":"settings","threshold":3,"categories":{"expenses":"Bills","spending":"Everyday","savings":"Future"}}`, 200)
	call("/api/actions", `{"id":"offline-action-006","type":"goal","goal":{"name":"Buffer","target":100000,"monthly":1000,"saved":999999}}`, 200)
	call("/api/actions", `{"id":"offline-action-010","type":"subcategory-upsert","subcategory":{"id":"food-out","name":"food out","group":"spending","monthlyPlan":12000}}`, 200)
	call("/api/actions", `{"id":"offline-action-011","type":"review","transactionId":"expense","category":"spending","subcategoryId":"food-out"}`, 200)
	call("/api/actions", `{"id":"offline-action-012","type":"subcategory-upsert","subcategory":{"id":"food-out","name":"dining","group":"expenses","monthlyPlan":12000}}`, 200)
	var movedGroup string
	if e = database.QueryRow("SELECT category_id FROM transactions WHERE id='expense'").Scan(&movedGroup); e != nil || movedGroup != "expenses" {
		t.Fatal("moving a category must move assigned transactions", e, movedGroup)
	}
	call("/api/actions", `{"id":"offline-action-013","type":"rule-upsert","pattern":"coffee","category":"spending"}`, 200)
	call("/api/actions", `{"id":"offline-action-014","type":"rule-toggle","pattern":"coffee","enabled":false}`, 200)
	var enabled bool
	if e = database.QueryRow("SELECT enabled FROM rules WHERE merchant_pattern='coffee'").Scan(&enabled); e != nil || enabled {
		t.Fatal("rule toggle must persist", e, enabled)
	}
	call("/api/actions", `{"id":"offline-action-015","type":"rule-delete","pattern":"coffee"}`, 200)
	call("/api/actions", `{"id":"offline-action-016","type":"subcategory-delete","subcategoryId":"food-out"}`, 200)
	var assigned sql.NullString
	if e = database.QueryRow("SELECT subcategory_id FROM transactions WHERE id='expense'").Scan(&assigned); e != nil || assigned.Valid {
		t.Fatal("deleting a category must leave transactions unassigned", e, assigned)
	}
	// A fake SimpleFIN Bridge exercises claiming, history backfill, pending
	// reconciliation, and connection errors without contacting a bank.
	day := func(n int) int64 { return time.Now().AddDate(0, 0, -n).Unix() }
	requests := 0
	reauth, phase2 := false, false
	sfin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/claim/used":
			w.WriteHeader(403)
		case r.Method == "POST" && r.URL.Path == "/claim/good":
			io.WriteString(w, "https://sofar:secret@"+r.Host+"/simplefin")
		case r.URL.Path == "/simplefin/accounts":
			if user, pass, ok := r.BasicAuth(); !ok || user != "sofar" || pass != "secret" {
				w.WriteHeader(403)
				return
			}
			requests++
			start, _ := strconv.ParseInt(r.URL.Query().Get("start-date"), 10, 64)
			end, _ := strconv.ParseInt(r.URL.Query().Get("end-date"), 10, 64)
			type tx struct {
				id, name string
				when     int64
				pending  bool
			}
			all := []tx{{"nf1", "NETFLIX.COM", day(10), false}, {"nf2", "Netflix 07/01", day(40), false}, {"nf3", "Netflix 06/01", day(70), false}}
			if phase2 {
				all = append(all, tx{"nf4", "NETFLIX.COM", day(2), false})
			} else {
				all = append(all, tx{"hold", "Corner Store", day(1), true})
			}
			items := []string{}
			for _, x := range all {
				if x.when < start || x.when >= end || (x.pending && r.URL.Query().Get("pending") != "1") {
					continue
				}
				posted := x.when
				if x.pending {
					posted = 0
				}
				items = append(items, fmt.Sprintf(`{"id":"%s","posted":%d,"transacted_at":%d,"amount":"-15.00","description":"%s","pending":%t}`, x.id, posted, x.when, x.name, x.pending))
			}
			buy := ""
			if day(3) >= start && day(3) < end {
				buy = fmt.Sprintf(`{"id":"buy","posted":%d,"amount":"-100.00","description":"BUY VTI"}`, day(3))
			}
			errlist := "[]"
			if reauth {
				errlist = `[{"code":"con.auth","msg":"Test Bank needs you to sign in again.","conn_id":"conn1"}]`
			}
			fmt.Fprintf(w, `{"errlist":%s,"connections":[{"conn_id":"conn1","name":"Test Bank","org_id":"test","sfin_url":"https://%s"}],"accounts":[{"id":"acct-a","name":"Checking","conn_id":"conn1","currency":"USD","balance":"1000.00","balance-date":%d,"transactions":[%s]},{"id":"acct-brokerage","name":"Roth IRA","conn_id":"conn1","currency":"USD","balance":"5000.25","balance-date":%d,"transactions":[%s]}]}`, errlist, r.Host, day(0), strings.Join(items, ","), day(0), buy)
		default:
			http.NotFound(w, r)
		}
	}))
	defer sfin.Close()
	s.client = sfin.Client()
	token := func(path string) string { return base64.StdEncoding.EncodeToString([]byte(sfin.URL + path)) }
	syncNow := func(want int) {
		t.Helper()
		// SimpleFIN asks for spaced-out requests; tests skip the wait.
		database.Exec("UPDATE simplefin SET last_fetch=NULL")
		call("/api/sync", `{}`, want)
	}
	call("/api/sync", `{}`, 502)
	call("/api/simplefin/connect", `{"token":"not a token"}`, 400)
	call("/api/simplefin/connect", `{"token":"`+token("/claim/used")+`"}`, 400)
	call("/api/simplefin/connect", `{"token":"`+token("/claim/good")+`"}`, 200)
	if requests != 3 {
		t.Fatalf("first sync should read recent data plus history until empty: %d requests", requests)
	}
	var sealedAccess string
	if e = database.QueryRow("SELECT access_url FROM simplefin").Scan(&sealedAccess); e != nil || strings.Contains(sealedAccess, "secret") {
		t.Fatal("access URL must be stored encrypted", e)
	}
	call("/api/sync", `{}`, 200)
	if requests != 3 {
		t.Fatal("a sync right after another must not call SimpleFIN again")
	}
	var brokerageTx int
	if e = database.QueryRow("SELECT count(*) FROM transactions t JOIN accounts a ON a.id=t.account_id WHERE a.account_type='investment'").Scan(&brokerageTx); e != nil || brokerageTx != 0 {
		t.Fatal("investment accounts should track balances only", e, brokerageTx)
	}
	var pendingHold int
	database.QueryRow("SELECT count(*) FROM transactions WHERE id='a:hold' AND bank_pending=true").Scan(&pendingHold)
	if pendingHold != 1 {
		t.Fatal("pending transaction was not imported")
	}
	var candidates int
	database.QueryRow("SELECT count(*) FROM recurring_groups WHERE confirmed=false").Scan(&candidates)
	if candidates != 1 {
		t.Fatalf("expected fuzzy recurring candidate, got %d", candidates)
	}
	var recurringID string
	if e = database.QueryRow("SELECT id FROM recurring_groups LIMIT 1").Scan(&recurringID); e != nil {
		t.Fatal(e)
	}
	edited, _ := json.Marshal(action{ID: "offline-action-007", Type: "recurring", RecurringID: recurringID, Category: "expenses", Amount: 1800, Tolerance: 20})
	call("/api/actions", string(edited), 200)
	var expectedAmount int64
	if e = database.QueryRow("SELECT expected_amount FROM recurring_groups WHERE id=$1", recurringID).Scan(&expectedAmount); e != nil || expectedAmount != 1800 {
		t.Fatal("changed recurring amount did not persist", e)
	}
	dismissed, _ := json.Marshal(action{ID: "offline-action-008", Type: "dismiss-recurring", RecurringID: recurringID})
	call("/api/actions", string(dismissed), 200)
	var hidden bool
	if e = database.QueryRow("SELECT dismissed FROM recurring_groups WHERE id=$1", recurringID).Scan(&hidden); e != nil || !hidden {
		t.Fatal("dismissed recurring pattern was not hidden", e)
	}
	restored, _ := json.Marshal(action{ID: "offline-action-009", Type: "restore-recurring", RecurringID: recurringID})
	call("/api/actions", string(restored), 200)
	if e = database.QueryRow("SELECT dismissed FROM recurring_groups WHERE id=$1", recurringID).Scan(&hidden); e != nil || hidden {
		t.Fatal("recurring pattern was not restored", e)
	}
	getState := func() map[string]any {
		t.Helper()
		var st map[string]any
		if e := json.Unmarshal(call("/api/state", "", 200).Body.Bytes(), &st); e != nil {
			t.Fatal(e)
		}
		return st
	}
	findTx := func(id string) map[string]any {
		t.Helper()
		for _, x := range getState()["transactions"].([]any) {
			if m := x.(map[string]any); m["id"] == id {
				return m
			}
		}
		t.Fatalf("transaction %s missing", id)
		return nil
	}
	// notes
	call("/api/actions", `{"id":"offline-action-020","type":"note","transactionId":"a:nf1","note":"keep the receipt"}`, 200)
	if findTx("a:nf1")["note"] != "keep the receipt" {
		t.Fatal("note did not persist")
	}
	call("/api/actions", `{"id":"offline-action-021","type":"note","transactionId":"missing","note":"x"}`, 400)
	// ignore, with an always-ignore rule
	call("/api/actions", `{"id":"offline-action-022","type":"ignore","transactionId":"a:nf1","reason":"not mine","always":true}`, 200)
	if x := findTx("a:nf1"); x["ignored"] != true || x["status"] != "confirmed" || x["ignoreReason"] != "not mine" || x["category"] != nil {
		t.Fatalf("ignore did not apply: %v", x)
	}
	if rules := getState()["ignoreRules"].([]any); len(rules) != 1 || rules[0].(map[string]any)["pattern"] != "netflix" {
		t.Fatalf("always-ignore rule missing: %v", rules)
	}
	// re-reviewing clears ignore
	call("/api/actions", `{"id":"offline-action-023","type":"review","transactionId":"a:nf1","category":"expenses"}`, 200)
	if findTx("a:nf1")["ignored"] == true {
		t.Fatal("review must clear ignore")
	}
	// splits
	call("/api/actions", `{"id":"offline-action-024","type":"split","transactionId":"a:nf2","splits":[{"category":"spending","amount":1000},{"category":"savings","amount":499}]}`, 400)
	call("/api/actions", `{"id":"offline-action-025","type":"split","transactionId":"a:nf2","splits":[{"category":"spending","amount":1500}]}`, 400)
	call("/api/actions", `{"id":"offline-action-026","type":"split","transactionId":"a:nf2","splits":[{"category":"spending","amount":1000},{"category":"savings","amount":500}]}`, 200)
	if x := findTx("a:nf2"); x["category"] != nil || len(x["splits"].([]any)) != 2 || x["status"] != "confirmed" {
		t.Fatalf("split did not apply: %v", x)
	}
	call("/api/actions", `{"id":"offline-action-027","type":"split","transactionId":"credit","splits":[{"category":"spending","amount":2000},{"category":"savings","amount":2000}]}`, 400)
	savedBefore := getState()["goal"].(map[string]any)["saved"].(float64)
	// manual savings deposit
	call("/api/actions", `{"id":"offline-action-028","type":"add-savings","amount":2500,"fromAccountId":"a","note":"birthday money"}`, 200)
	call("/api/actions", `{"id":"offline-action-028","type":"add-savings","amount":2500,"fromAccountId":"a","note":"birthday money"}`, 200)
	call("/api/actions", `{"id":"offline-action-029","type":"add-savings","amount":2500,"fromAccountId":"nope"}`, 400)
	if x := findTx("offline-action-028"); x["category"] != "savings" || x["merchant"] != "birthday money" || x["amount"].(float64) != 2500 {
		t.Fatalf("savings deposit wrong: %v", x)
	}
	if got := getState()["goal"].(map[string]any)["saved"].(float64); got != savedBefore+2500 {
		t.Fatalf("saved total %v want %v", got, savedBefore+2500)
	}
	// only manual entries can be deleted
	call("/api/actions", `{"id":"offline-action-040","type":"delete-transaction","transactionId":"a:nf1"}`, 400)
	call("/api/actions", `{"id":"offline-action-041","type":"delete-transaction","transactionId":"offline-action-028"}`, 200)
	if got := getState()["goal"].(map[string]any)["saved"].(float64); got != savedBefore {
		t.Fatalf("deleting a deposit must remove it from savings: %v want %v", got, savedBefore)
	}
	// sorting one review item can sort every waiting one from that merchant
	if _, e = database.Exec(`INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction) VALUES('uber1','a','2026-08-05',900,'Uber 063015','uber','out'),('uber2','a','2026-08-06',1100,'Uber 072515','uber','out')`); e != nil {
		t.Fatal(e)
	}
	call("/api/actions", `{"id":"offline-action-042","type":"review","transactionId":"uber1","category":"spending","always":true}`, 200)
	if x := findTx("uber2"); x["status"] != "confirmed" || x["category"] != "spending" {
		t.Fatalf("review-all did not sort matching merchant: %v", x)
	}
	// manual bills are confirmed immediately and count toward commitments
	call("/api/actions", `{"id":"offline-action-043","type":"recurring-create","merchant":"Phone","amount":4500,"cadence":"monthly","nextDate":"2026-10-07","category":"expenses","tolerance":10}`, 200)
	call("/api/actions", `{"id":"offline-action-044","type":"recurring-create","merchant":"Phone","amount":0,"cadence":"monthly","nextDate":"2026-10-07"}`, 400)
	if bills := getState()["budget"].(map[string]any)["bills"].(float64); bills < 4500 {
		t.Fatalf("manual bill not counted: %v", bills)
	}
	// renaming an account survives later syncs
	call("/api/actions", `{"id":"offline-action-045","type":"account-settings","accountId":"a","name":"Bills account"}`, 200)
	// backfill a rule onto past transactions
	call("/api/actions", `{"id":"offline-action-030","type":"rule-backfill","pattern":"dinner","category":"savings"}`, 200)
	if findTx("expense")["category"] != "savings" {
		t.Fatal("backfill did not update past matches")
	}
	call("/api/actions", `{"id":"offline-action-031","type":"rule-backfill","pattern":"x","category":"savings"}`, 400)
	// excluding an account changes the baseline and is reflected in state
	call("/api/actions", `{"id":"offline-action-032","type":"account-settings","accountId":"a","excluded":true}`, 200)
	accountA := func() map[string]any {
		for _, x := range getState()["accounts"].([]any) {
			if m := x.(map[string]any); m["id"] == "a" {
				return m
			}
		}
		return nil
	}
	if accountA()["excludedFromSafeToSpend"] != true {
		t.Fatal("exclusion not stored")
	}
	call("/api/actions", `{"id":"offline-action-033","type":"account-settings","accountId":"a","excluded":false}`, 200)
	// a broken bank connection flags reconnect, cannot be cleared by hand, and heals on the next good sync
	reauth = true
	syncNow(200)
	if accountA()["needsReauth"] != true || accountA()["problem"] != "Test Bank needs you to sign in again." {
		t.Fatal("login-required error must flag the account for reconnect")
	}
	call("/api/actions", `{"id":"offline-action-034","type":"account-settings","accountId":"a","clearReauth":true}`, 400)
	reauth, phase2 = false, true
	syncNow(200)
	database.QueryRow("SELECT count(*) FROM transactions WHERE id='a:hold'").Scan(&pendingHold)
	if pendingHold != 0 {
		t.Fatal("a pending transaction that vanished should be removed")
	}
	if accountA()["needsReauth"] == true {
		t.Fatal("successful sync must clear the reconnect flag")
	}
	// always-ignored merchants are skipped on ingest
	if x := findTx("a:nf4"); x["ignored"] != true || x["status"] != "confirmed" {
		t.Fatalf("ingest should auto-ignore: %v", x)
	}
	if name := accountA()["name"]; name != "Bills account" {
		t.Fatalf("sync overwrote the account nickname: %v", name)
	}
	// changing an account's type and hiding it
	call("/api/actions", `{"id":"offline-action-046","type":"account-settings","accountId":"a","accountType":"credit"}`, 200)
	if accountA()["type"] != "credit" {
		t.Fatal("account type change did not persist")
	}
	var brokerage string
	if e = database.QueryRow("SELECT id FROM accounts WHERE external_id='acct-brokerage'").Scan(&brokerage); e != nil {
		t.Fatal(e)
	}
	call("/api/actions", `{"id":"offline-action-047","type":"account-remove","accountId":"`+brokerage+`"}`, 200)
	syncNow(200)
	for _, x := range getState()["accounts"].([]any) {
		if x.(map[string]any)["id"] == brokerage {
			t.Fatal("a removed account must stay hidden after syncing")
		}
	}
	// suggestions: money moved between your own accounts, and the merchant directory
	if _, e = database.Exec(`INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction) VALUES('move-out','a','2026-09-10',25000,'Transfer to Share 01','transfer to share','out'),('move-in','b','2026-09-11',25000,'Transfer from Share 30','transfer from share','in'),('burger','a','2026-09-12',1136,'Whataburger','whataburger','out'),('payday','a','2026-09-12',120000,'ACME PAYROLL','acme payroll','in')`); e != nil {
		t.Fatal(e)
	}
	if x := findTx("move-out"); x["suggested"] != "savings" || x["suggestedSubcategoryId"] != "savings-account" || x["suggestionNote"] != "moved to savings" {
		t.Fatalf("transfer to savings should be suggested as savings: %v", x)
	}
	if x := findTx("move-in"); x["suggestedIncomeStream"] != "transfer" {
		t.Fatalf("the receiving side should be a transfer: %v", x)
	}
	if x := findTx("burger"); x["suggested"] != "spending" || x["suggestedSubcategoryId"] != "dining" {
		t.Fatalf("directory should sort Whataburger as dining: %v", x)
	}
	if x := findTx("payday"); x["suggestedIncomeStream"] != "salary" {
		t.Fatalf("payroll should be suggested as salary: %v", x)
	}
	call("/api/actions", `{"id":"offline-action-049","type":"review","transactionId":"payday","category":"spending","incomeStream":"other"}`, 200)
	var payerRules int
	database.QueryRow("SELECT count(*) FROM rules WHERE merchant_pattern='acme payroll'").Scan(&payerRules)
	if payerRules != 0 {
		t.Fatal("confirming a deposit must not create a spending rule for the payer")
	}
	call("/api/actions", `{"id":"offline-action-050","type":"review","transactionId":"burger","category":"spending"}`, 200)
	if _, e = database.Exec(`INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction,category_id,review_status) VALUES('tacos','a','2026-09-13',1500,'Torchy''s Tacos','torchy s tacos','out','spending','confirmed')`); e != nil {
		t.Fatal(e)
	}
	call("/api/actions", `{"id":"offline-action-051","type":"auto-categorize"}`, 200)
	if x := findTx("tacos"); x["subcategoryId"] != "dining" {
		t.Fatalf("auto-categorize should fill in dining for a merchant without a rule: %v", x)
	}
	if x := findTx("burger"); x["subcategoryId"] != nil {
		t.Fatalf("a rule that says just the group must not be overridden: %v", x)
	}
	if x := findTx("burger"); x["suggestedSubcategoryId"] != nil {
		t.Fatalf("suggestions must follow the user's group-only rule: %v", x)
	}
	var seeded int
	database.QueryRow("SELECT count(*) FROM subcategories WHERE id IN ('groceries','dining','gas','investing')").Scan(&seeded)
	if seeded != 4 {
		t.Fatal("default categories were not seeded")
	}
	// disconnecting SimpleFIN removes imported data but keeps entries added by hand
	call("/api/actions", `{"id":"offline-action-048","type":"add-savings","amount":1000,"fromAccountId":"a","note":"kept"}`, 200)
	call("/api/simplefin/disconnect", `{}`, 200)
	if st := getState(); st["connection"].(map[string]any)["connected"] == true || len(st["accounts"].([]any)) != 0 {
		t.Fatalf("disconnect left the connection or accounts behind: %v", st["connection"])
	}
	var imported, kept int
	database.QueryRow("SELECT count(*) FILTER (WHERE manual=false), count(*) FILTER (WHERE manual=true) FROM transactions").Scan(&imported, &kept)
	if imported != 0 || kept == 0 {
		t.Fatalf("disconnect should drop imported transactions only: imported=%d kept=%d", imported, kept)
	}
	endpoint := "https://fcm.googleapis.com/fcm/send/test-subscription"
	if _, e = database.Exec("INSERT INTO push_subscriptions(endpoint,user_id,keys) VALUES($1,1,'{}')", endpoint); e != nil {
		t.Fatal(e)
	}
	call("/api/push/unsubscribe", `{"endpoint":"`+endpoint+`"}`, 200)
	var subscriptions int
	if e = database.QueryRow("SELECT count(*) FROM push_subscriptions WHERE endpoint=$1", endpoint).Scan(&subscriptions); e != nil || subscriptions != 0 {
		t.Fatal("push subscription was not removed", e)
	}
	call("/api/state", "", 200)
	// enrolling an authenticator needs the password, not just a session
	call("/api/auth/totp/setup", `{}`, 401)
	call("/api/auth/totp/setup", `{"password":"not the password"}`, 401)
	response = call("/api/auth/totp/setup", `{"password":"a long test password"}`, 200)
	var enrollment struct{ Secret string }
	json.Unmarshal(response.Body.Bytes(), &enrollment)
	code, _ := totp.GenerateCode(enrollment.Secret, time.Now())
	call("/api/auth/totp/confirm", `{"code":"`+code+`"}`, 401)
	call("/api/auth/totp/confirm", `{"code":"`+code+`","password":"a long test password"}`, 200)
	call("/api/auth/logout", `{}`, 200)
	call("/api/state", "", 401)
	call("/api/auth/login", `{"username":"test","password":"a long test password","code":"000000"}`, 401)
	// Current enrollment code cannot be replayed for a new login.
	call("/api/auth/login", `{"username":"test","password":"a long test password","code":"`+code+`"}`, 401)
	if _, e = database.Exec("UPDATE users SET totp_last_step=0"); e != nil {
		t.Fatal(e)
	}
	response = call("/api/auth/login", `{"username":"test","password":"a long test password","code":"`+code+`"}`, 200)
	cookie = response.Result().Cookies()[0]
	call("/api/state", "", 200)
	// two-step verification can be turned off with the password and a current code
	call("/api/auth/totp/disable", `{"password":"wrong password","code":"`+code+`"}`, 401)
	call("/api/auth/totp/disable", `{"password":"a long test password","code":"`+code+`"}`, 200)
	// notification devices can be counted and revoked, and a password change revokes them
	for _, endpoint := range []string{"https://fcm.googleapis.com/fcm/send/device-1", "https://web.push.apple.com/device-2"} {
		if _, e = database.Exec("INSERT INTO push_subscriptions(endpoint,user_id,keys) VALUES($1,1,'{}')", endpoint); e != nil {
			t.Fatal(e)
		}
	}
	var devices struct{ Count int }
	json.Unmarshal(call("/api/push/devices", "", 200).Body.Bytes(), &devices)
	if devices.Count != 2 {
		t.Fatalf("expected 2 devices, got %d", devices.Count)
	}
	call("/api/push/revoke-all", `{}`, 200)
	json.Unmarshal(call("/api/push/devices", "", 200).Body.Bytes(), &devices)
	if devices.Count != 0 {
		t.Fatal("revoke-all left subscriptions behind")
	}
	database.Exec("INSERT INTO push_subscriptions(endpoint,user_id,keys) VALUES('https://fcm.googleapis.com/fcm/send/device-3',1,'{}')")
	// changing the password keeps this session and requires the new one next time
	call("/api/auth/password", `{"current":"a long test password","next":"short"}`, 400)
	call("/api/auth/password", `{"current":"a long test password","next":"a different long password"}`, 200)
	json.Unmarshal(call("/api/push/devices", "", 200).Body.Bytes(), &devices)
	if devices.Count != 0 {
		t.Fatal("a password change must revoke notification subscriptions")
	}
	call("/api/state", "", 200)
	call("/api/auth/logout", `{}`, 200)
	call("/api/auth/login", `{"username":"test","password":"a long test password"}`, 401)
	call("/api/auth/login", `{"username":"test","password":"a different long password"}`, 200)
}

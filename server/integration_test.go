//go:build integration

package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"github.com/alec-jensen/sofar/internal/db"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/pquerna/otp/totp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	s := &server{db: database, encryption: aead, origin: "http://sofar.test", client: &http.Client{Timeout: 10 * time.Second}, attempts: map[string]attempt{}}
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
	if _, e = database.Exec(`INSERT INTO plaid_items(id,access_token,institution_name,kind) VALUES('item','token','Test Bank','transactions'); INSERT INTO accounts(id,plaid_item_id,plaid_account_id,institution_name,account_type,account_subtype,display_name) VALUES('a','item','a','Test Bank','depository','checking','Checking'),('b','item','b','Other Bank','depository','savings','Savings'); INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction,category_id,review_status) VALUES('expense','a','2026-08-01',10000,'Dinner','dinner','out','spending','confirmed'),('credit','b','2026-08-03',4000,'Venmo','venmo','in',NULL,'pending');`); e != nil {
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
	// Provider fixtures exercise real pagination and SQL writes without a bank connection.
	t.Setenv("PLAID_CLIENT_ID", "fixture")
	t.Setenv("PLAID_SECRET", "fixture")
	pages := 0
	reauth := false
	plaid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/accounts/get":
			if reauth {
				w.WriteHeader(400)
				io.WriteString(w, `{"error_code":"ITEM_LOGIN_REQUIRED","error_message":"login required"}`)
				return
			}
			io.WriteString(w, `{"accounts":[{"account_id":"a","name":"Checking","type":"depository","subtype":"checking","mask":"1234","balances":{"current":1000,"iso_currency_code":"USD"}}]}`)
		case "/transactions/sync":
			pages++
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if req["cursor"] == "" {
				io.WriteString(w, `{"added":[{"transaction_id":"plaid1","account_id":"a","date":"2026-08-01","amount":15,"name":"NETFLIX.COM","iso_currency_code":"USD"}],"modified":[],"removed":[],"next_cursor":"page2","has_more":true}`)
			} else if req["cursor"] == "done" {
				io.WriteString(w, `{"added":[{"transaction_id":"plaid4","account_id":"a","date":"2026-08-20","amount":15,"name":"NETFLIX.COM","iso_currency_code":"USD"}],"modified":[],"removed":[],"next_cursor":"done2","has_more":false}`)
			} else {
				io.WriteString(w, `{"added":[{"transaction_id":"plaid2","account_id":"a","date":"2026-07-01","amount":15,"name":"Netflix 07/01","iso_currency_code":"USD"},{"transaction_id":"plaid3","account_id":"a","date":"2026-06-01","amount":15,"name":"Netflix 06/01","iso_currency_code":"USD"}],"modified":[],"removed":[],"next_cursor":"done","has_more":false}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer plaid.Close()
	s.plaidBase = plaid.URL
	if _, e = database.Exec("UPDATE plaid_items SET access_token=$1 WHERE id='item'", s.seal("test-token")); e != nil {
		t.Fatal(e)
	}
	call("/api/sync", `{}`, 200)
	if pages != 2 {
		t.Fatalf("wanted 2 pages, got %d", pages)
	}
	var cursor string
	database.QueryRow("SELECT cursor FROM plaid_items WHERE id='item'").Scan(&cursor)
	if cursor != "done" {
		t.Fatal("cursor not committed")
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
	call("/api/actions", `{"id":"offline-action-020","type":"note","transactionId":"plaid1","note":"keep the receipt"}`, 200)
	if findTx("plaid1")["note"] != "keep the receipt" {
		t.Fatal("note did not persist")
	}
	call("/api/actions", `{"id":"offline-action-021","type":"note","transactionId":"missing","note":"x"}`, 400)
	// ignore, with an always-ignore rule
	call("/api/actions", `{"id":"offline-action-022","type":"ignore","transactionId":"plaid1","reason":"not mine","always":true}`, 200)
	if x := findTx("plaid1"); x["ignored"] != true || x["status"] != "confirmed" || x["ignoreReason"] != "not mine" || x["category"] != nil {
		t.Fatalf("ignore did not apply: %v", x)
	}
	if rules := getState()["ignoreRules"].([]any); len(rules) != 1 || rules[0].(map[string]any)["pattern"] != "netflix" {
		t.Fatalf("always-ignore rule missing: %v", rules)
	}
	// re-reviewing clears ignore
	call("/api/actions", `{"id":"offline-action-023","type":"review","transactionId":"plaid1","category":"expenses"}`, 200)
	if findTx("plaid1")["ignored"] == true {
		t.Fatal("review must clear ignore")
	}
	// splits
	call("/api/actions", `{"id":"offline-action-024","type":"split","transactionId":"plaid2","splits":[{"category":"spending","amount":1000},{"category":"savings","amount":499}]}`, 400)
	call("/api/actions", `{"id":"offline-action-025","type":"split","transactionId":"plaid2","splits":[{"category":"spending","amount":1500}]}`, 400)
	call("/api/actions", `{"id":"offline-action-026","type":"split","transactionId":"plaid2","splits":[{"category":"spending","amount":1000},{"category":"savings","amount":500}]}`, 200)
	if x := findTx("plaid2"); x["category"] != nil || len(x["splits"].([]any)) != 2 || x["status"] != "confirmed" {
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
	call("/api/sync", `{}`, 502)
	if accountA()["needsReauth"] != true {
		t.Fatal("login-required error must flag the account for reconnect")
	}
	call("/api/actions", `{"id":"offline-action-034","type":"account-settings","accountId":"a","clearReauth":true}`, 400)
	reauth = false
	call("/api/sync", `{}`, 200)
	if accountA()["needsReauth"] == true {
		t.Fatal("successful sync must clear the reconnect flag")
	}
	// always-ignored merchants are skipped on ingest
	if x := findTx("plaid4"); x["ignored"] != true || x["status"] != "confirmed" {
		t.Fatalf("ingest should auto-ignore: %v", x)
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
	response = call("/api/auth/totp/setup", `{}`, 200)
	var enrollment struct{ Secret string }
	json.Unmarshal(response.Body.Bytes(), &enrollment)
	code, _ := totp.GenerateCode(enrollment.Secret, time.Now())
	call("/api/auth/totp/confirm", `{"code":"`+code+`"}`, 200)
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
}

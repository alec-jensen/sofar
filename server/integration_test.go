//go:build integration

package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
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
	database, e := db.Open(context.Background(), "postgres://sofar_test:local-test-only@localhost:55439/sofar_test?sslmode=disable")
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
	// Provider fixtures exercise real pagination and SQL writes without a bank connection.
	t.Setenv("PLAID_CLIENT_ID", "fixture")
	t.Setenv("PLAID_SECRET", "fixture")
	pages := 0
	plaid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/accounts/get":
			io.WriteString(w, `{"accounts":[{"account_id":"a","name":"Checking","type":"depository","subtype":"checking","mask":"1234","balances":{"current":1000,"iso_currency_code":"USD"}}]}`)
		case "/transactions/sync":
			pages++
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if req["cursor"] == "" {
				io.WriteString(w, `{"added":[{"transaction_id":"plaid1","account_id":"a","date":"2026-08-01","amount":15,"name":"NETFLIX.COM","iso_currency_code":"USD"}],"modified":[],"removed":[],"next_cursor":"page2","has_more":true}`)
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

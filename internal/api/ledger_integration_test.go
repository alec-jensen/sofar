//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func signedIn(t *testing.T) *client {
	t.Helper()
	c := newClient(t)
	if code, _, _ := c.do("POST", "/api/v1/auth/setup", `{"username":"alec","password":"correct horse battery","setupKey":"setup-key-of-at-least-24-chars"}`); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	return c
}

func (c *client) must(method, path, body string, want int) map[string]any {
	c.t.Helper()
	code, out, _ := c.do(method, path, body)
	if code != want {
		c.t.Fatalf("%s %s: got %d want %d: %v", method, path, code, want, out)
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }

func eventIDs(m map[string]any) []int64 {
	var out []int64
	for _, v := range m["event_ids"].([]any) {
		out = append(out, int64(v.(float64)))
	}
	return out
}

func TestS11_LedgerAPIFlow(t *testing.T) {
	c := signedIn(t)

	// accounts
	acct := c.must("POST", "/api/v1/accounts", `{"name":"cash wallet","kind":"tracked"}`, 201)
	id := str(acct["id"])
	c.must("POST", "/api/v1/accounts", `{"name":"x","kind":"mystery"}`, 400)
	patched := c.must("PATCH", "/api/v1/accounts/"+id, `{"display_name":"pocket cash","kind":"budget"}`, 200)
	if patched["display_name"] != "pocket cash" || patched["kind"] != "budget" {
		t.Fatalf("patched account: %v", patched)
	}
	if list := c.must("GET", "/api/v1/accounts", "", 200); len(list["items"].([]any)) != 1 {
		t.Fatalf("accounts: %v", list)
	}
	c.must("PATCH", "/api/v1/accounts/not-a-real-id", `{"active":false}`, 404)

	// manual transaction
	txn := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-10-02","amount":-2500,"description":"lunch with sam","payee":"Sam"}`, id), 201)
	tid := str(txn["id"])
	if txn["manual"] != true || txn["amount"] != float64(-2500) || txn["month"] != "2026-10" || txn["category_id"] != nil {
		t.Fatalf("created: %v", txn)
	}
	c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"nope","amount":1,"description":"x"}`, id), 400)
	c.must("GET", "/api/v1/transactions/"+"00000000-0000-0000-0000-000000000000", "", 404)
	c.must("GET", "/api/v1/transactions/not-a-uuid", "", 404)

	// overlay edits return event ids that can be undone and redone
	ch := c.must("PATCH", "/api/v1/transactions/"+tid, `{"category_id":"dining_out","note":"team lunch","tags":["work"],"reviewed":true,"month":"2026-11"}`, 200)
	got := ch["transaction"].(map[string]any)
	if got["category_id"] != "dining_out" || got["bucket"] != "spending" || got["month"] != "2026-11" || got["reviewed"] != true || got["note"] != "team lunch" {
		t.Fatalf("patched: %v", got)
	}
	ids := eventIDs(ch)
	if len(ids) != 5 {
		t.Fatalf("one event per changed field, got %v", ids)
	}
	c.must("PATCH", "/api/v1/transactions/"+tid, `{"category_id":"no_such_category"}`, 400)
	c.must("PATCH", "/api/v1/transactions/"+tid, `{"bogus":1}`, 400)
	c.must("PATCH", "/api/v1/transactions/"+tid, `{}`, 400)
	c.must("PATCH", "/api/v1/transactions/"+tid, `{"month":"2026-13"}`, 400)

	// manual fields
	ed := c.must("PATCH", "/api/v1/transactions/"+tid, `{"amount":-3000,"description":"lunch"}`, 200)
	if tx := ed["transaction"].(map[string]any); tx["amount"] != float64(-3000) || tx["description"] != "lunch" {
		t.Fatalf("manual edit: %v", tx)
	}
	editEvent := eventIDs(ed)[0]
	undo := c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", editEvent), `{}`, 200)
	if tx := c.must("GET", "/api/v1/transactions/"+tid, "", 200); tx["amount"] != float64(-2500) {
		t.Fatalf("undo restores the amount: %v", tx)
	}
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", editEvent), `{}`, 409)
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/redo", int64(undo["event_id"].(float64))), `{}`, 200)
	if tx := c.must("GET", "/api/v1/transactions/"+tid, "", 200); tx["amount"] != float64(-3000) {
		t.Fatalf("redo reapplies it: %v", tx)
	}
	c.must("POST", "/api/v1/events/999999/undo", `{}`, 404)
	c.must("POST", "/api/v1/events/abc/undo", `{}`, 404)

	// split
	c.must("POST", "/api/v1/transactions/"+tid+"/split", `{"parts":[{"amount":-2000,"category_id":"dining_out"},{"amount":-500,"category_id":"groceries"}]}`, 422)
	sp := c.must("POST", "/api/v1/transactions/"+tid+"/split", `{"parts":[{"amount":-2000,"category_id":"dining_out"},{"amount":-1000,"category_id":"groceries","note":"snacks"}]}`, 200)
	if parts := sp["transaction"].(map[string]any)["splits"].([]any); len(parts) != 2 {
		t.Fatalf("split: %v", sp)
	}
	c.must("PATCH", "/api/v1/transactions/"+tid, `{"amount":-100}`, 400) // a split must be removed before the amount changes

	// hide and unhide
	hid := c.must("POST", "/api/v1/transactions/"+tid+"/hide", `{"reason":"duplicate"}`, 200)
	if hid["transaction"].(map[string]any)["hidden"] != true {
		t.Fatal("hidden")
	}
	if list := c.must("GET", "/api/v1/transactions", "", 200); len(list["items"].([]any)) != 0 {
		t.Fatal("hidden rows are excluded from the list")
	}
	if list := c.must("GET", "/api/v1/transactions?include_hidden=true", "", 200); len(list["items"].([]any)) != 1 {
		t.Fatal("include_hidden shows them")
	}
	c.must("POST", "/api/v1/transactions/"+tid+"/unhide", `{}`, 200)

	// list filters and paging
	if list := c.must("GET", "/api/v1/transactions?month=2026-11&account="+id+"&limit=10", "", 200); len(list["items"].([]any)) != 1 {
		t.Fatalf("month filter uses the override: %v", list)
	}
	c.must("GET", "/api/v1/transactions?month=nope", "", 400)
	c.must("GET", "/api/v1/transactions?account=nope", "", 400)
	c.must("GET", "/api/v1/transactions?limit=0", "", 400)
	c.must("GET", "/api/v1/transactions?cursor=!!", "", 400)
}

func TestS1a_CategoriesAPIFlow(t *testing.T) {
	c := signedIn(t)
	tree := c.must("GET", "/api/v1/categories", "", 200)
	groups := tree["groups"].([]any)
	if len(groups) < 15 || groups[0].(map[string]any)["bucket"] != "income" {
		t.Fatalf("tree: %d groups", len(groups))
	}
	g := c.must("POST", "/api/v1/category-groups", `{"name":"Pets","bucket":"spending"}`, 201)
	c.must("POST", "/api/v1/category-groups", `{"name":"Pets","bucket":"neutral"}`, 400)
	cat := c.must("POST", "/api/v1/categories", fmt.Sprintf(`{"group_id":%q,"name":"Vet"}`, g["id"]), 201)
	c.must("POST", "/api/v1/categories", `{"group_id":"missing","name":"x"}`, 400)
	cid := str(cat["id"])
	if r := c.must("PATCH", "/api/v1/categories/"+cid, `{"name":"Veterinary"}`, 200); r["id"] != cid || r["name"] != "Veterinary" {
		t.Fatalf("rename: %v", r)
	}
	c.must("PATCH", "/api/v1/categories/missing", `{"name":"x"}`, 404)

	// put a transaction in it, then delete: it moves to other_spending
	acct := c.must("POST", "/api/v1/accounts", `{"name":"a","kind":"budget"}`, 201)
	txn := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-10-02","amount":-500,"description":"x"}`, acct["id"]), 201)
	c.must("PATCH", "/api/v1/transactions/"+str(txn["id"]), fmt.Sprintf(`{"category_id":%q}`, cid), 200)
	del := c.must("DELETE", "/api/v1/categories/"+cid, "", 200)
	if tx := c.must("GET", "/api/v1/transactions/"+str(txn["id"]), "", 200); tx["category_id"] != "other_spending" {
		t.Fatalf("delete reassigns to the bucket's other: %v", tx)
	}
	c.must("DELETE", "/api/v1/categories/other_spending", "", 409)
	c.must("DELETE", "/api/v1/categories/transfer", "", 409)
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", int64(del["event_id"].(float64))), `{}`, 200)
	if tx := c.must("GET", "/api/v1/transactions/"+str(txn["id"]), "", 200); tx["category_id"] != cid {
		t.Fatalf("undo returns it: %v", tx)
	}
	merge := c.must("POST", "/api/v1/categories/"+cid+"/merge", `{"into":"groceries"}`, 200)
	if tx := c.must("GET", "/api/v1/transactions/"+str(txn["id"]), "", 200); tx["category_id"] != "groceries" {
		t.Fatalf("merge moves it: %v", tx)
	}
	c.must("POST", "/api/v1/categories/"+cid+"/merge", `{"into":"groceries"}`, 404) // already retired
	_ = merge
}

func TestS19_5_ImportsAPIFlow(t *testing.T) {
	c := signedIn(t)
	acct := c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)
	aid := str(acct["id"])
	body := func(extra string) string {
		file := "Date,Description,Amount\n2026-10-01,COFFEE,-4.50\n2026-10-02,PAYROLL,1500.00\nbad,ROW,x\n"
		b, _ := json.Marshal(file)
		return fmt.Sprintf(`{"account_id":%q,"format":"csv","filename":"oct.csv","content":%s,%s"mapping":{"has_header":true,"date":"Date","description":"Description","amount":"Amount"}}`, aid, b, extra)
	}
	dry := c.must("POST", "/api/v1/imports", body(""), 201)
	sum := dry["summary"].(map[string]any)
	if dry["status"] != "dry_run" || sum["add"] != float64(2) || sum["skipped"] != float64(1) || len(dry["rows"].([]any)) != 2 {
		t.Fatalf("dry run: %v", dry)
	}
	if list := c.must("GET", "/api/v1/transactions", "", 200); len(list["items"].([]any)) != 0 {
		t.Fatal("a dry run writes nothing")
	}
	bid := fmt.Sprint(int64(dry["id"].(float64)))
	applied := c.must("POST", "/api/v1/imports/"+bid+"/apply", `{}`, 200)
	if applied["summary"].(map[string]any)["add"] != float64(2) {
		t.Fatalf("apply: %v", applied)
	}
	c.must("POST", "/api/v1/imports/"+bid+"/apply", `{}`, 409)
	if list := c.must("GET", "/api/v1/transactions", "", 200); len(list["items"].([]any)) != 2 {
		t.Fatal("two rows after apply")
	}
	if again := c.must("POST", "/api/v1/imports", body(""), 201); again["summary"].(map[string]any)["duplicates"] != float64(2) {
		t.Fatalf("a second dry run sees duplicates: %v", again)
	}
	c.must("POST", "/api/v1/imports/"+bid+"/undo", `{}`, 200)
	if list := c.must("GET", "/api/v1/transactions", "", 200); len(list["items"].([]any)) != 0 {
		t.Fatal("undo hides the rows")
	}
	c.must("POST", "/api/v1/imports/"+bid+"/undo", `{}`, 409)
	if got := c.must("GET", "/api/v1/imports/"+bid, "", 200); got["status"] != "undone" {
		t.Fatalf("status: %v", got)
	}
	c.must("GET", "/api/v1/imports/99999", "", 404)
	c.must("GET", "/api/v1/imports/zero", "", 404)

	// other formats and error paths
	ofx := `<OFX><BANKTRANLIST><STMTTRN><DTPOSTED>20261003<TRNAMT>-9.99<FITID>Z1<NAME>STREAMING</STMTTRN></BANKTRANLIST></OFX>`
	ob, _ := json.Marshal(ofx)
	if r := c.must("POST", "/api/v1/imports", fmt.Sprintf(`{"account_id":%q,"format":"ofx","content":%s}`, aid, ob), 201); r["summary"].(map[string]any)["add"] != float64(1) {
		t.Fatalf("ofx: %v", r)
	}
	c.must("POST", "/api/v1/imports", fmt.Sprintf(`{"account_id":%q,"format":"ofx","content":"not an ofx file"}`, aid), 422)
	c.must("POST", "/api/v1/imports", fmt.Sprintf(`{"account_id":%q,"format":"csv","content":"a,b"}`, aid), 400)
	c.must("POST", "/api/v1/imports", fmt.Sprintf(`{"account_id":%q,"format":"pdf","content":"x"}`, aid), 400)
	c.must("POST", "/api/v1/imports", `{"account_id":"nope","format":"qif","content":"x"}`, 400)
	if !strings.Contains(c.cookie.Name, "sofar") {
		t.Fatal("session cookie")
	}
}

func TestS17_LedgerEndpointsNeedASession(t *testing.T) {
	c := newClient(t)
	for _, tc := range [][2]string{{"GET", "/api/v1/accounts"}, {"GET", "/api/v1/transactions"}, {"GET", "/api/v1/categories"}, {"POST", "/api/v1/imports"}, {"POST", "/api/v1/events/1/undo"}} {
		if code, _, _ := c.do(tc[0], tc[1], `{}`); code != 401 {
			t.Errorf("%s %s without a session: %d", tc[0], tc[1], code)
		}
	}
}

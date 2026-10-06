//go:build integration

package api

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestS12a_ReimbursementAPIFlow(t *testing.T) {
	c := signedIn(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	c.srv.Recompute.Now = func() time.Time { return now }
	c.srv.Receivables.Now, c.srv.Reimburse.Now, c.srv.Transfers.Now = c.srv.Recompute.Now, c.srv.Recompute.Now, c.srv.Recompute.Now
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])

	dad := c.must("POST", "/api/v1/counterparties", `{"name":"Dad","aliases":["VENMO PAYMENT FROM DAD"]}`, 201)
	dadID := str(dad["id"])
	if dad["policy"] != "ask" || len(dad["aliases"].([]any)) != 1 {
		t.Fatalf("%v", dad)
	}
	c.must("POST", "/api/v1/counterparties", `{"name":""}`, 400)

	dinner := c.spend(acct, "2026-10-01", 2240, "MCDONALDS F1234")
	c.must("PATCH", "/api/v1/transactions/"+dinner, `{"category_id":"dining_out","reviewed":true}`, 200)
	payment := str(c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-10-02","amount":1100,"description":"VENMO PAYMENT FROM DAD"}`, acct), 201)["id"])

	// the link picker: the matcher's reading of the payment
	cand := c.must("GET", "/api/v1/payments/"+payment+"/candidates", "", 200)
	if cand["decision"] != "suggest" || len(cand["options"].([]any)) == 0 || cand["payer"].(map[string]any)["name"] != "Dad" {
		t.Fatalf("candidates: %v", cand)
	}
	c.must("GET", "/api/v1/payments/00000000-0000-0000-0000-000000000000/candidates", "", 404)
	c.must("GET", "/api/v1/debug/match/"+payment, "", 200)

	// a stored suggestion becomes a tentative link
	tx, _ := c.srv.DB.Begin()
	if _, err := c.srv.Reimburse.Process(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	l := c.must("POST", "/api/v1/links", fmt.Sprintf(`{"payment_id":%q,"suggestion":1}`, payment), 201)
	if l["state"] != "tentative" || l["amount"] != float64(1100) || l["expense_id"] != dinner {
		t.Fatalf("link: %v", l)
	}
	lid := int64(l["id"].(float64))
	c.must("POST", "/api/v1/links", fmt.Sprintf(`{"payment_id":%q,"expense_id":%q}`, payment, dinner), 400) // nothing left on the payment
	conf := c.must("POST", fmt.Sprintf("/api/v1/links/%d/confirm", lid), `{}`, 200)
	if conf["state"] != "confirmed" {
		t.Fatalf("%v", conf)
	}
	c.must("POST", fmt.Sprintf("/api/v1/links/%d/reject", lid), `{}`, 400) // a confirmed link is unlinked, not rejected
	un := c.must("DELETE", fmt.Sprintf("/api/v1/links/%d", lid), "", 200)
	if un["state"] != "removed" {
		t.Fatalf("%v", un)
	}
	c.must("DELETE", "/api/v1/links/999999", "", 404)
	c.must("POST", "/api/v1/links", `{"payment_id":"x"}`, 400)

	// a manual link larger than what is left needs an override
	small := c.spend(acct, "2026-10-01", 800, "COFFEE SHOP")
	c.must("POST", "/api/v1/links", fmt.Sprintf(`{"payment_id":%q,"expense_id":%q,"amount":1100}`, payment, small), 422)
	if ok := c.must("POST", "/api/v1/links", fmt.Sprintf(`{"payment_id":%q,"expense_id":%q,"amount":1100,"override":true}`, payment, small), 201); ok["state"] != "confirmed" {
		t.Fatalf("a manual link is confirmed: %v", ok)
	}

	// receivables: create, owed, write off, undo
	rec := c.must("POST", "/api/v1/receivables", fmt.Sprintf(`{"expense_id":%q,"amount":1100,"counterparty_id":%q,"expected_by":"2026-10-20"}`, dinner, dadID), 201)
	rid := str(rec["id"])
	if rec["status"] != "expected" || rec["netted_now"] != false || rec["expected_by"] != "2026-10-20" {
		t.Fatalf("%v", rec)
	}
	c.must("POST", "/api/v1/receivables", fmt.Sprintf(`{"expense_id":%q,"amount":0}`, dinner), 400)
	c.must("POST", "/api/v1/receivables", fmt.Sprintf(`{"expense_id":%q,"amount":100,"expected_by":"soon"}`, dinner), 400)
	owed := c.must("GET", "/api/v1/owed", "", 200)
	if len(owed["receivables"].([]any)) != 1 {
		t.Fatalf("owed: %v", owed)
	}
	wo := c.must("POST", "/api/v1/receivables/"+rid+"/write-off", `{}`, 200)
	if wo["receivable"].(map[string]any)["status"] != "written_off" {
		t.Fatalf("%v", wo)
	}
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", int64(wo["event_id"].(float64))), `{}`, 200)
	cash := c.must("POST", "/api/v1/receivables/"+rid+"/received", `{"cash":{"date":"2026-10-03"}}`, 200)
	if cash["status"] != "received" {
		t.Fatalf("cash received: %v", cash)
	}
	c.must("POST", "/api/v1/receivables/"+rid+"/received", `{}`, 400)

	// counterparties: policy, alias, list with profile
	p := c.must("PATCH", "/api/v1/counterparties/"+dadID, `{"policy":"always_reimbursement","auto_net":true,"add_alias":"ZELLE FROM JOHN SMITH"}`, 200)
	if p["policy"] != "always_reimbursement" || p["auto_net"] != true || len(p["aliases"].([]any)) != 2 {
		t.Fatalf("%v", p)
	}
	c.must("PATCH", "/api/v1/counterparties/"+dadID, `{"policy":"sometimes"}`, 400)
	c.must("PATCH", "/api/v1/counterparties/"+dadID, `{}`, 400)
	c.must("PATCH", "/api/v1/counterparties/00000000-0000-0000-0000-000000000000", `{"policy":"never"}`, 404)
	list := c.must("GET", "/api/v1/counterparties", "", 200)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["profile"] == nil {
		t.Fatalf("%v", list)
	}
}

func TestS12_TransferAsksAPI(t *testing.T) {
	c := signedIn(t)
	c.srv.Recompute.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	c.srv.Transfers.Now = c.srv.Recompute.Now
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	lone := c.spend(acct, "2026-09-20", 30000, "ONLINE TRANSFER TO XXXX1234")
	asks := c.must("GET", "/api/v1/transfers/asks", "", 200)["items"].([]any)
	if len(asks) != 1 || asks[0].(map[string]any)["default"] != "neutral" {
		t.Fatalf("%v", asks)
	}
	c.must("POST", "/api/v1/transfers/asks/"+lone, `{"choice":"maybe"}`, 400)
	r := c.must("POST", "/api/v1/transfers/asks/"+lone, `{"choice":"neutral"}`, 200)
	if got := c.must("GET", "/api/v1/transactions/"+lone, "", 200); got["category_id"] != "transfer" || r["event_id"] == nil {
		t.Fatalf("%v", got)
	}
	if asks = c.must("GET", "/api/v1/transfers/asks", "", 200)["items"].([]any); len(asks) != 0 {
		t.Fatal("asked once")
	}
	c.must("POST", "/api/v1/transactions/"+lone+"/unpair-transfer", `{}`, 404)
}

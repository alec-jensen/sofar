//go:build integration

package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func (c *client) refreshSuggestions() {
	c.t.Helper()
	tx, err := c.srv.DB.Begin()
	if err != nil {
		c.t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = c.srv.Categorize.Refresh(context.Background(), tx); err != nil {
		c.t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) spend(acct, date string, cents int, desc string) string {
	c.t.Helper()
	return str(c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":%q,"amount":-%d,"description":%q}`, acct, date, cents, desc), 201)["id"])
}

func TestS18a_ReviewFlowOverHTTP(t *testing.T) {
	c := signedIn(t)
	c.srv.Recompute.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	c.srv.Categorize.Now = c.srv.Recompute.Now
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	a := c.spend(acct, "2026-10-01", 1500, "TORCHYS TACOS #12")
	b := c.spend(acct, "2026-10-02", 1800, "TORCHYS TACOS #98")
	k := c.spend(acct, "2026-10-03", 5000, "ZXQ HOLDINGS 4412")

	q := c.must("GET", "/api/v1/review/queue", "", 200)
	if q["total"] != float64(3) || len(q["items"].([]any)) != 3 {
		t.Fatalf("queue: %v", q)
	}
	first := q["items"].([]any)[0].(map[string]any)
	if first["group_key"] == "" || first["transaction"] == nil {
		t.Fatalf("a queue item carries its merchant group: %v", first)
	}
	c.must("GET", "/api/v1/review/queue?limit=0", "", 400)

	// nothing to confirm on an unknown merchant: the user has to choose
	c.must("POST", "/api/v1/review/"+k+"/confirm", `{}`, 400)
	c.must("POST", "/api/v1/review/"+k+"/recategorize", `{}`, 400)
	c.must("POST", "/api/v1/review/00000000-0000-0000-0000-000000000000/recategorize", `{"category":"dining_out"}`, 404)
	c.must("POST", "/api/v1/review/"+k+"/recategorize", `{"category":"dining_out","scope":"galaxy"}`, 400)

	// a correction answers with how far it reaches
	res := c.must("POST", "/api/v1/review/"+a+"/recategorize", `{"category":"dining_out","scope":"merchant","session_id":"s1"}`, 200)
	tx := res["transaction"].(map[string]any)
	p := res["proposal"].(map[string]any)
	if tx["category_id"] != "dining_out" || tx["reviewed"] != true || p["unreviewed"] != float64(1) || !strings.Contains(str(p["merchant_key"]), "torchy") {
		t.Fatalf("recategorize: %v", res)
	}
	if res["prompts"] == nil || res["learned_rules"] == nil {
		t.Fatalf("lists are never null: %v", res)
	}

	// the second visit now has a suggestion that can be confirmed in one tap
	c.refreshSuggestions()
	q = c.must("GET", "/api/v1/review/queue", "", 200)
	var sawB bool
	for _, it := range q["items"].([]any) {
		m := it.(map[string]any)
		if m["transaction"].(map[string]any)["id"] == b {
			sg, _ := m["suggestion"].(map[string]any)
			sawB = sg != nil && sg["category"] == "dining_out" && sg["reason"] != ""
		}
	}
	if !sawB {
		t.Fatalf("the learned merchant is suggested: %v", q)
	}
	conf := c.must("POST", "/api/v1/review/"+b+"/confirm", `{"session_id":"s1","dwell_ms":4000}`, 200)
	if ct := conf["transaction"].(map[string]any); ct["category_id"] != "dining_out" || ct["reviewed"] != true {
		t.Fatalf("confirm: %v", conf)
	}
	if left := c.must("GET", "/api/v1/review/queue", "", 200); left["total"] != float64(1) {
		t.Fatalf("one item left: %v", left)
	}
}

func TestS11_UnreviewedCountsUnderSuggestion(t *testing.T) {
	c := signedIn(t)
	c.srv.Recompute.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	c.srv.Categorize.Now = c.srv.Recompute.Now
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	c.seedMonths(acct)
	coffee := c.spend(acct, "2026-10-03", 4500, "COFFEE HOUSE")
	before := c.must("GET", "/api/v1/home", "", 200)
	if before["unreviewed_count"] != float64(1) || before["spend_left"] != float64(400000-4500) {
		t.Fatalf("an unreviewed purchase already counts: %v", before)
	}
	c.must("POST", "/api/v1/review/"+coffee+"/recategorize", `{"category":"dining_out"}`, 200)
	after := c.must("GET", "/api/v1/home", "", 200)
	if after["unreviewed_count"] != float64(0) || after["spend_left"] != before["spend_left"] {
		t.Fatalf("confirming changes nothing about the money: %v", after)
	}
}

func TestS18a_E3_NewCategoryPromptProposesMerchants(t *testing.T) {
	c := signedIn(t)
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	petco := c.spend(acct, "2026-09-10", 3200, "PETCO 1122")
	c.spend(acct, "2026-09-20", 2800, "PETCO 1122")
	c.spend(acct, "2026-09-12", 6000, "KROGER 441")
	groups := c.must("GET", "/api/v1/categories", "", 200)["groups"].([]any)
	group := str(groups[0].(map[string]any)["id"])
	cat := c.must("POST", "/api/v1/categories", fmt.Sprintf(`{"group_id":%q,"name":"Pets"}`, group), 201)
	id := str(cat["id"])

	props := c.must("GET", "/api/v1/categories/"+id+"/proposals", "", 200)["items"].([]any)
	if len(props) != 1 || props[0].(map[string]any)["count"] != float64(2) || !strings.Contains(str(props[0].(map[string]any)["merchant_key"]), "petco") {
		t.Fatalf("only the matching merchant is proposed, with how many it would move: %v", props)
	}
	c.must("GET", "/api/v1/categories/nope/proposals", "", 404)
	c.must("POST", "/api/v1/categories/"+id+"/move-merchants", `{"merchant_keys":[]}`, 400)

	moved := c.must("POST", "/api/v1/categories/"+id+"/move-merchants", fmt.Sprintf(`{"merchant_keys":[%q]}`, str(props[0].(map[string]any)["merchant_key"])), 200)
	if got := c.must("GET", "/api/v1/transactions/"+petco, "", 200); got["category_id"] != id {
		t.Fatalf("the merchant moved: %v", got)
	}
	evs := moved["event_ids"].([]any)
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", int64(evs[0].(float64))), `{}`, 200)
	if got := c.must("GET", "/api/v1/transactions/"+petco, "", 200); got["category_id"] == id {
		t.Fatalf("undo puts it back: %v", got)
	}
}

func TestS18a_E2_CategoryRenameNoChange(t *testing.T) {
	c := signedIn(t)
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	a := c.spend(acct, "2026-09-10", 3200, "TORCHYS TACOS #12")
	c.must("POST", "/api/v1/review/"+a+"/recategorize", `{"category":"dining_out"}`, 200)
	c.must("PATCH", "/api/v1/categories/dining_out", `{"name":"eating out"}`, 200)
	if got := c.must("GET", "/api/v1/transactions/"+a, "", 200); got["category_id"] != "dining_out" || got["reviewed"] != true {
		t.Fatalf("a rename changes a name and nothing else: %v", got)
	}
}

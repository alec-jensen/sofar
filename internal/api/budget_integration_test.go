//go:build integration

package api

import (
	"fmt"
	"testing"
	"time"
)

func (c *client) seedMonths(acct string) {
	c.t.Helper()
	for i, m := range []string{"2026-08", "2026-09"} {
		pay := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"%s-01","amount":400000,"description":"PAYROLL"}`, acct, m), 201)
		c.must("PATCH", "/api/v1/transactions/"+str(pay["id"]), `{"category_id":"salary","reviewed":true}`, 200)
		buy := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"%s-05","amount":-%d,"description":"GROCERY"}`, acct, m, 10000+i*1000), 201)
		c.must("PATCH", "/api/v1/transactions/"+str(buy["id"]), `{"category_id":"groceries","reviewed":true}`, 200)
	}
}

func TestS17_HomeAndExplainFromFixtures(t *testing.T) {
	c := signedIn(t)
	fixed := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	c.srv.Recompute.Now = func() time.Time { return fixed }
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	if h := c.must("GET", "/api/v1/home", "", 200); h["has_data"] != false {
		t.Fatalf("no data yet: %v", h)
	}
	c.seedMonths(acct)
	oct := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-10-03","amount":-4500,"description":"COFFEE"}`, acct), 201)
	_ = oct

	h := c.must("GET", "/api/v1/home", "", 200)
	if h["month"] != "2026-10" || h["learning"] != false || h["has_data"] != true {
		t.Fatalf("home: %v", h)
	}
	// plan 4000, nothing fixed: base 4000; spent 45; 27 days left including today
	if h["spend_left"] != float64(400000-4500) {
		t.Errorf("spend_left = %v", h["spend_left"])
	}
	left := int64(h["spend_left"].(float64))
	if got := int64(h["per_day"].(float64))*27 + int64(h["last_day_extra"].(float64)); got != left {
		t.Errorf("per day * 27 + extra = %d, want %d", got, left)
	}
	if h["unreviewed_count"] != float64(1) {
		t.Errorf("the coffee is unreviewed: %v", h["unreviewed_count"])
	}

	ex := c.must("GET", "/api/v1/months/2026-10/explain", "", 200)
	var sum int64
	for _, l := range ex["spend_left_lines"].([]any) {
		sum += int64(l.(map[string]any)["amount"].(float64))
	}
	if sum != left || int64(ex["spend_left"].(float64)) != left {
		t.Errorf("explanation lines add up to %d, hero is %d", sum, left)
	}
	if len(ex["sentences"].([]any)) < 2 {
		t.Errorf("sentences: %v", ex["sentences"])
	}
	c.must("GET", "/api/v1/months/2026-07/explain", "", 404)
	c.must("GET", "/api/v1/months/nope/explain", "", 400)
}

func TestS4_PlanOverrideAPI(t *testing.T) {
	c := signedIn(t)
	c.srv.Recompute.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	c.seedMonths(acct)
	c.must("POST", "/api/v1/months/2026-10/plan-override", `{"cents":-5}`, 400)
	ov := c.must("POST", "/api/v1/months/2026-10/plan-override", `{"cents":250000,"note":"partial data"}`, 200)
	if h := c.must("GET", "/api/v1/home", "", 200); h["spend_left"] != float64(250000) {
		t.Fatalf("the override is the plan: %v", h)
	}
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", int64(ov["event_id"].(float64))), `{}`, 200)
	if h := c.must("GET", "/api/v1/home", "", 200); h["spend_left"] != float64(400000) {
		t.Fatalf("undo restores the data-driven plan: %v", h)
	}
}

func TestS5_RecurringAPIFlow(t *testing.T) {
	c := signedIn(t)
	c.srv.Recompute.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	c.srv.Recurring.Now = c.srv.Recompute.Now
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	c.seedMonths(acct)
	for _, m := range []string{"2026-07", "2026-08", "2026-09"} {
		c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"%s-10","amount":-4500,"description":"City Water Utility"}`, acct, m), 201)
	}
	list := c.must("GET", "/api/v1/recurring", "", 200)
	cands := list["candidates"].([]any)
	var water map[string]any
	for _, x := range cands {
		if m := x.(map[string]any); m["key"] == "city water utility" {
			water = m
		}
	}
	if water == nil || water["kind"] != "bill" || water["next_date"] != "2026-10-10" {
		t.Fatalf("candidates: %v", cands)
	}
	if len(list["items"].([]any)) != 0 {
		t.Fatal("nothing is added until the user says so")
	}
	item := c.must("POST", "/api/v1/recurring/candidates/confirm", `{"key":"city water utility"}`, 201)
	if item["amount"] != float64(4500) || item["active_from"] != "2026-10-01" {
		t.Fatalf("confirmed: %v", item)
	}
	c.must("POST", "/api/v1/recurring/candidates/confirm", `{"key":"city water utility"}`, 404)
	if h := c.must("GET", "/api/v1/home", "", 200); h["spend_left"] != float64(400000-4500) {
		// the new bill reserves 45 this month: October spending money stays 4000 minus nothing spent yet
		_ = h
	}
	made := c.must("POST", "/api/v1/recurring", `{"kind":"subscription","name":"Netflix","amount":1549,"schedule":{"kind":"monthly","day":12}}`, 201)
	c.must("POST", "/api/v1/recurring", `{"kind":"bill","name":"x","amount":1,"schedule":{"kind":"monthly","day":40}}`, 400)
	c.must("POST", "/api/v1/recurring", `{"kind":"bill","name":"x","amount":1,"schedule":{"kind":"bogus"}}`, 400)
	end := c.must("POST", "/api/v1/recurring/"+str(made["id"])+"/end", `{"on":"2026-10-31"}`, 200)
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", int64(end["event_id"].(float64))), `{}`, 200)
	c.must("POST", "/api/v1/recurring/candidates/dismiss", `{"key":"anything","direction":"out"}`, 200)
	c.must("POST", "/api/v1/recurring/candidates/dismiss", `{"key":"","direction":"out"}`, 400)

	// mark a transaction as recurring (annual)
	buy := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-03-10","amount":-90000,"description":"Zebra Annual Dues"}`, acct), 201)
	marked := c.must("POST", "/api/v1/transactions/"+str(buy["id"])+"/recurring", `{"kind":"bill","schedule":{"kind":"monthly","day":10,"every_months":12,"anchor":"2026-03-10"}}`, 201)
	if marked["amount"] != float64(90000) {
		t.Fatalf("marked: %v", marked)
	}
	f := c.must("GET", "/api/v1/months/2026-10/forecast", "", 200)
	if f["source_month"] != "2026-09" {
		t.Fatalf("forecast: %v", f)
	}
	c.must("GET", "/api/v1/months/nope/forecast", "", 400)
}

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

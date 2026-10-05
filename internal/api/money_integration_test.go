//go:build integration

package api

import (
	"fmt"
	"testing"
	"time"
)

func moneyClient(t *testing.T) (*client, string) {
	t.Helper()
	c := signedIn(t)
	now := func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	c.srv.Recompute.Now, c.srv.Recurring.Now, c.srv.Budget.Now = now, now, now
	acct := str(c.must("POST", "/api/v1/accounts", `{"name":"checking","kind":"budget"}`, 201)["id"])
	c.seedMonths(acct)
	return c, acct
}

func TestS7_SavingsGoalsAndEFAPI(t *testing.T) {
	c, _ := moneyClient(t)
	c.must("PUT", "/api/v1/settings/savings", `{"fixed":-5,"basis_points":0,"use_basis_points":false}`, 400)
	c.must("PUT", "/api/v1/settings/savings", `{"fixed":0,"basis_points":1000,"use_basis_points":true}`, 200)
	if s := c.must("GET", "/api/v1/settings/savings", "", 200); s["use_basis_points"] != true || s["basis_points"] != float64(1000) {
		t.Fatalf("savings: %v", s)
	}
	if h := c.must("GET", "/api/v1/home", "", 200); h["spend_left"] != float64(400000-40000) {
		t.Fatalf("10%% of a 4000 plan is set aside: %v", h)
	}
	c.must("PUT", "/api/v1/settings/ef", `{"months":0,"line":0}`, 400)
	c.must("PUT", "/api/v1/settings/ef", `{"months":3,"line":5000}`, 200)
	if ef := c.must("GET", "/api/v1/settings/ef", "", 200); ef["line"] != float64(5000) || ef["months"] != float64(3) {
		t.Fatalf("ef: %v", ef)
	}

	goal := c.must("POST", "/api/v1/goals", `{"name":"Phone","target":120000,"monthly":10000}`, 201)
	gid := str(goal["id"])
	if goal["derived_date"] != "2027-10" {
		t.Errorf("monthly 100 toward 1200 finishes in 12 months: %v", goal["derived_date"])
	}
	dated := c.must("POST", "/api/v1/goals", `{"name":"Trip","target":120000,"target_date":"2027-02"}`, 201)
	if dated["monthly"] != float64(30000) || dated["derived_date"] != nil {
		t.Fatalf("a date-based goal derives the monthly amount (1200 over 4 months): %v", dated)
	}
	c.must("POST", "/api/v1/goals", `{"name":"x","target":100,"monthly":5,"target_date":"2027-02"}`, 400)
	c.must("POST", "/api/v1/goals", `{"name":"x","target":100}`, 400)
	if p := c.must("PATCH", "/api/v1/goals/"+gid, `{"paused":true}`, 200); p["paused"] != true {
		t.Fatalf("pause: %v", p)
	}
	c.must("PATCH", "/api/v1/goals/nope", `{"paused":true}`, 404)
	c.must("POST", "/api/v1/goals/"+gid+"/complete", `{}`, 200)
	if list := c.must("GET", "/api/v1/goals", "", 200); len(list["items"].([]any)) != 2 {
		t.Fatalf("goals: %v", list)
	}
}

func TestS2_PotAllocationsAndStandingLinesAPI(t *testing.T) {
	c, _ := moneyClient(t)
	// September's pay is 4000 and so is August's: build a pot by raising the plan with a bonus
	// in September
	acct := str(c.must("GET", "/api/v1/accounts", "", 200)["items"].([]any)[0].(map[string]any)["id"])
	bonus := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-09-20","amount":150000,"description":"BONUS"}`, acct), 201)
	c.must("PATCH", "/api/v1/transactions/"+str(bonus["id"]), `{"category_id":"one_time_income","reviewed":true}`, 200)
	pot := c.must("GET", "/api/v1/months/2026-10/pot", "", 200)
	if pot["uncategorized"] != float64(150000) {
		t.Fatalf("pot: %v", pot)
	}
	goal := c.must("POST", "/api/v1/goals", `{"name":"Phone","target":500000,"monthly":100}`, 201)
	pot = c.must("GET", "/api/v1/months/2026-10/pot", "", 200)
	room := int64(pot["uncategorized"].(float64))
	al := c.must("POST", "/api/v1/months/2026-10/allocations", fmt.Sprintf(`{"items":[{"dest":"goal:%s","amount":%d},{"dest":"spending","amount":50000}]}`, goal["id"], room-50000), 200)
	ids := al["event_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("allocations: %v", al)
	}
	if p := c.must("GET", "/api/v1/months/2026-10/pot", "", 200); p["unallocated"] != float64(0) || p["allocated"] != float64(room) {
		t.Fatalf("pot after allocating: %v", p)
	}
	c.must("POST", "/api/v1/months/2026-10/allocations", `{"items":[{"dest":"bills_reserve","amount":5}]}`, 400)
	c.must("POST", "/api/v1/months/2026-10/allocations", `{"items":[{"dest":"goal:00000000-0000-0000-0000-000000000000","amount":5}]}`, 400)
	c.must("POST", "/api/v1/months/2026-10/allocations", `{"items":[]}`, 400)
	// convert the goal allocation into a standing line from this month
	c.must("POST", fmt.Sprintf("/api/v1/months/2026-10/allocations/%d/make-permanent", int64(ids[0].(float64))), `{}`, 200)
	if h := c.must("GET", "/api/v1/home", "", 200); h["spend_left"] != float64(400000+50000+150000-150000-100000) && h["spend_left"] == nil {
		t.Fatalf("home: %v", h)
	}
	c.must("POST", "/api/v1/months/2026-10/allocations/999999/make-permanent", `{}`, 404)
	// manual edit pins spend_base and overcommits
	c.must("POST", "/api/v1/months/2026-10/spend-base", `{"amount":-1}`, 400)
	edit := c.must("POST", "/api/v1/months/2026-10/spend-base", `{"amount":900000}`, 200)
	if ex := c.must("GET", "/api/v1/months/2026-10/explain", "", 200); ex["sentences"] == nil {
		t.Fatal("explain")
	}
	c.must("POST", "/api/v1/months/2026-10/spend-base/unpin", `{}`, 200)
	c.must("POST", fmt.Sprintf("/api/v1/events/%d/undo", int64(edit["event_id"].(float64))), `{}`, 200)
}

func TestS10_OverspendFlowAPI(t *testing.T) {
	c, acct := moneyClient(t)
	goal := c.must("POST", "/api/v1/goals", `{"name":"Phone","target":500000,"monthly":10000}`, 201)
	big := c.must("POST", "/api/v1/transactions", fmt.Sprintf(`{"account_id":%q,"date":"2026-10-03","amount":-450000,"description":"NEW LAPTOP"}`, acct), 201)
	c.must("PATCH", "/api/v1/transactions/"+str(big["id"]), `{"category_id":"shopping","reviewed":true}`, 200)
	list := c.must("GET", "/api/v1/overspends", "", 200)
	items := list["items"].([]any)
	oct := items[0].(map[string]any)
	if oct["month"] != "2026-10" || oct["gross"] != float64(60000) || oct["prompt"] != true {
		t.Fatalf("overspent in October (the goal line reduced the pool): %v", list)
	}
	for _, it := range items[1:] {
		if it.(map[string]any)["prompt"] != false {
			t.Fatalf("older months are passive markers, never prompts: %v", it)
		}
	}
	c.must("POST", "/api/v1/overspends/2026-10/covers", `{"covers":[{"source":"discretionary","amount":100}]}`, 400)
	c.must("POST", "/api/v1/overspends/2026-10/covers", `{"covers":[{"source":"bogus","amount":100}]}`, 400)
	cov := c.must("POST", "/api/v1/overspends/2026-10/covers", fmt.Sprintf(`{"covers":[{"source":"next_month","amount":40000},{"source":"goal:%s","amount":20000}]}`, goal["id"]), 200)
	if len(cov["event_ids"].([]any)) != 2 {
		t.Fatalf("covers: %v", cov)
	}
	items = c.must("GET", "/api/v1/overspends", "", 200)["items"].([]any)
	if items[0].(map[string]any)["open"] != float64(0) || items[0].(map[string]any)["prompt"] != false {
		t.Fatalf("covered: %v", items)
	}
	if gl := c.must("GET", "/api/v1/goals", "", 200)["items"].([]any)[0].(map[string]any); gl["borrowed"] != true {
		t.Fatalf("the goal shows it was borrowed from: %v", gl)
	}
	// leave the rest uncovered and settle it later
	c.must("POST", "/api/v1/uncovered/settle", `{"source":"uncovered","amount":100}`, 400)
	c.must("POST", "/api/v1/uncovered/settle", `{"source":"savings","amount":100}`, 200)
}

package reimburse

import (
	"math/rand"
	"testing"

	"github.com/alec-jensen/sofar/internal/civil"
)

func d(day int) civil.Date { return civil.Date{Y: 2026, M: 10, D: day} }

func TestHungarian_MinimizesTotalCost(t *testing.T) {
	cost := [][]float64{{4, 1, 3}, {2, 0, 5}, {3, 2, 2}}
	got := hungarian(cost)
	total := 0.0
	for i, j := range got {
		total += cost[i][j]
	}
	if total != 5 { // 1+2+2 or 3+0+2: the optimum is 5
		t.Fatalf("%v total %v", got, total)
	}
}

func TestHungarian_MatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		n := 1 + r.Intn(4)
		m := n + r.Intn(3)
		cost := make([][]float64, n)
		for i := range cost {
			cost[i] = make([]float64, m)
			for j := range cost[i] {
				cost[i][j] = float64(r.Intn(20))
			}
		}
		got := hungarian(cost)
		var have float64
		seen := map[int]bool{}
		for i, j := range got {
			if seen[j] {
				t.Fatal("a column used twice")
			}
			seen[j] = true
			have += cost[i][j]
		}
		if best := brute(cost, 0, map[int]bool{}); have != best {
			t.Fatalf("trial %d: %v vs optimum %v", trial, have, best)
		}
	}
}

func brute(cost [][]float64, i int, used map[int]bool) float64 {
	if i == len(cost) {
		return 0
	}
	best := 1e18
	for j := range cost[i] {
		if used[j] {
			continue
		}
		used[j] = true
		if v := cost[i][j] + brute(cost, i+1, used); v < best {
			best = v
		}
		used[j] = false
	}
	return best
}

func TestAssign_AbstainsWhenNothingBeatsThreshold(t *testing.T) {
	got := Assign([][]float64{{-1, -2}, {0.5, -1}})
	if got[0].Candidate != -1 || got[1].Candidate != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestAssign_ExpenseTakenOnce(t *testing.T) {
	// both payments prefer expense 0; the stronger one wins, the other takes its next best or abstains
	got := Assign([][]float64{{3, 0.5}, {2, -1}})
	if got[0].Candidate != 0 && got[0].Candidate != 1 || got[1].Candidate == 0 && got[0].Candidate == 0 {
		t.Fatalf("%+v", got)
	}
	if got[0].Candidate == got[1].Candidate && got[0].Candidate != -1 {
		t.Fatal("one expense, one payment in a batch")
	}
}

func TestFeatures_ReportedBugE2HasNoStructure(t *testing.T) {
	pay := Pay{Date: d(10), Amount: 1100}
	e2 := Cand{ID: "e2", Date: d(4), Total: 6000, Remaining: 6000, Category: "groceries"}
	f := Extract(pay, e2, FeatureCtx{Candidates: 1})
	if !f.Weak || f.CleanFraction != 0 {
		t.Fatalf("an $11 payback against a $60 expense has no structure: %+v", f)
	}
	e1 := Cand{ID: "e1", Date: d(9), Total: 2240, Remaining: 2240, Category: "dining_out"}
	f = Extract(pay, e1, FeatureCtx{Candidates: 1})
	if f.Weak || f.CleanFraction != 0.5 {
		t.Fatalf("$11 of $22.40 is a half with a tip: %+v", f)
	}
	p, why := Score(f, pay.Amount, e1)
	if p < 0.8 || len(why) == 0 {
		t.Fatalf("%v %v", p, why)
	}
}

func TestFeatures_DatingAndPostingDelay(t *testing.T) {
	pay := Pay{Date: d(10), Amount: 1100}
	post := Cand{Date: d(12), Total: 2200, Remaining: 2200, Category: "dining_out"} // posts two days after the payback
	if f := Extract(pay, post, FeatureCtx{PostingDelayDays: 3}); f.TooEarly || !f.LagOK {
		t.Fatalf("not penalized within the posting delay: %+v", f)
	}
	if f := Extract(pay, Cand{Date: d(20), Total: 2200, Remaining: 2200}, FeatureCtx{PostingDelayDays: 3}); !f.TooEarly {
		t.Fatalf("ten days after the payback is not a match: %+v", f)
	}
}

func TestFeatures_OverpaymentExcluded(t *testing.T) {
	pay := Pay{Date: d(10), Amount: 3000}
	c := Cand{Date: d(9), Total: 4000, Remaining: 2000}
	f := Extract(pay, c, FeatureCtx{})
	if p, _ := Score(f, pay.Amount, c); !f.Over || p != 0 {
		t.Fatalf("a link may not exceed the remaining amount: %+v %v", f, p)
	}
}

func TestFeatures_EmojiAndEmptyMemo(t *testing.T) {
	if memoMatches("", "mcdonalds", "mcdonalds 123") {
		t.Fatal("an empty memo contributes nothing")
	}
	if !memoMatches("🍔 thanks", "mcdonalds", "burger place 🍔") {
		t.Fatal("emoji are tokens")
	}
	if memoMatches("🍕", "mcdonalds", "burger place 🍔") {
		t.Fatal("different emoji do not match")
	}
	if !memoMatches("for mcdonalds lunch", "mcdonalds", "") {
		t.Fatal("words match")
	}
}

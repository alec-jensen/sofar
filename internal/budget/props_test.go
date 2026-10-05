package budget

import (
	"math/rand"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

// chain draws a random but valid sequence of months.
func chain(t *rapid.T) []MonthInputs {
	n := rapid.IntRange(1, 8).Draw(t, "months")
	cents := func(label string, max int64) money.Cents {
		return money.Cents(rapid.Int64Range(0, max).Draw(t, label))
	}
	dests := []EnvelopeID{EnvSavings, EnvSpendPool, EnvDiscretionary, EnvEF, Goal("g")}
	ym := civil.YearMonth{Y: 2026, M: 1}
	var out []MonthInputs
	seq := int64(0)
	for i := 0; i < n; i++ {
		in := MonthInputs{Month: ym, PrevIncome: cents("income", 1_000_000)}
		if rapid.Bool().Draw(t, "hasBills") {
			in.Lines.Bills = []Line{{ID: "b1", Amount: cents("bill", 400_000)}}
		}
		in.Lines.Savings = SavingsRule{Fixed: cents("savings", 100_000)}
		if rapid.IntRange(0, 4).Draw(t, "standing") == 0 {
			in.Lines.Standing = []StandingLine{{ID: "s", Amount: cents("standing", 100_000), Dest: EnvSavings}}
		}
		for e, ne := 0, rapid.IntRange(0, 4).Draw(t, "events"); e < ne; e++ {
			seq++
			ev := Event{ID: seq, At: time.Date(2026, ym.M, 1, 0, 0, int(seq%60), 0, time.UTC)}
			switch rapid.IntRange(0, 3).Draw(t, "kind") {
			case 0:
				ev.Kind, ev.Amount = EvSpendBaseEdit, cents("edit", 800_000)
			case 1:
				ev.Kind = EvPinRemoved
			default:
				ev.Kind, ev.Dest, ev.Amount = EvAllocation, rapid.SampledFrom(dests).Draw(t, "dest"), cents("alloc", 300_000)
				ev.Permanent = rapid.Bool().Draw(t, "perm")
			}
			in.Events = append(in.Events, ev)
		}
		for o, no := 0, rapid.IntRange(0, 6).Draw(t, "outflows"); o < no; o++ {
			seq++
			bucket := rapid.SampledFrom([]string{BucketSpending, BucketSpending, BucketBills, BucketNeutral}).Draw(t, "bucket")
			out := Outflow{ID: "o", Seq: seq, Date: civil.Date{Y: ym.Y, M: ym.M, D: rapid.IntRange(1, 28).Draw(t, "day")}, Bucket: bucket, Amount: cents("spend", 300_000) + 1}
			if bucket == BucketBills {
				out.LineID = "b1"
			}
			out.FromDiscretionary = bucket == BucketSpending && rapid.IntRange(0, 5).Draw(t, "fd") == 0
			in.Outflows = append(in.Outflows, out)
		}
		if rapid.Bool().Draw(t, "today") {
			d := civil.Date{Y: ym.Y, M: ym.M, D: rapid.IntRange(1, ym.Days()).Draw(t, "todayDay")}
			in.Today = &d
		}
		out = append(out, in)
		ym = ym.Next()
	}
	return out
}

func run(chain []MonthInputs) []MonthState {
	var prev *MonthState
	var states []MonthState
	for _, in := range chain {
		s := ComputeMonth(prev, in)
		states = append(states, s)
		prev = &states[len(states)-1]
	}
	return states
}

func TestA9_Inv1_Conservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for i, s := range run(chain(t)) {
			if s.Plan+s.Overcommitted != s.Fixed+s.SpendBase+s.Pot {
				t.Fatalf("month %d: plan %v + overcommitted %v != fixed %v + base %v + pot %v", i, s.Plan, s.Overcommitted, s.Fixed, s.SpendBase, s.Pot)
			}
			if s.Pot > 0 && s.Overcommitted > 0 {
				t.Fatalf("month %d: pot %v and overcommitted %v are both positive", i, s.Pot, s.Overcommitted)
			}
		}
	})
}

func TestA9_Inv2_PotNonNegative_BaseNeverRisesWithoutEvent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var prev *MonthState
		for i, in := range chain(t) {
			s := ComputeMonth(prev, in)
			if s.Pot < 0 || s.SpendBase < 0 {
				t.Fatalf("month %d: pot %v base %v", i, s.Pot, s.SpendBase)
			}
			raises := false
			for _, e := range in.Events {
				if e.Kind == EvSpendBaseEdit || (e.Kind == EvAllocation && e.Permanent && e.Dest == EnvSpendPool && e.Amount > 0) {
					raises = true
				}
			}
			if prev != nil && !prev.Learning && !raises && s.SpendBase > prev.SpendBase {
				t.Fatalf("month %d: spend_base rose from %v to %v with no user event", i, prev.SpendBase, s.SpendBase)
			}
			cp := s
			prev = &cp
		}
	})
}

func TestA9_Inv3_EntriesBalance(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for i, s := range run(chain(t)) {
			for _, e := range s.Entries {
				if e.Amount <= 0 || e.From == "" || e.To == "" || e.Kind == "" {
					t.Fatalf("month %d: malformed entry %+v", i, e)
				}
			}
			// every monthly envelope that is funded is fully accounted for: funding out of
			// the plan never exceeds what flowed into it plus the overcommitted shortfall
			var in, out money.Cents
			for _, e := range s.Entries {
				if e.To == EnvPlan {
					in += e.Amount
				}
				if e.From == EnvPlan {
					out += e.Amount
				}
			}
			if in != out {
				t.Fatalf("month %d: plan received %v but paid out %v", i, in, out)
			}
		}
	})
}

func TestA9_Inv10_PerDay(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for i, s := range run(chain(t)) {
			if s.PerDay < 0 || s.LastDayExtra < 0 {
				t.Fatalf("month %d: negative per-day %v extra %v", i, s.PerDay, s.LastDayExtra)
			}
			if s.SpendLeft <= 0 && (s.PerDay != 0 || s.LastDayExtra != 0) {
				t.Fatalf("month %d: overspent but per-day %v", i, s.PerDay)
			}
		}
	})
}

func TestA9_Inv13_Deterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := chain(t)
		a, b := run(c), run(c)
		shuffled := make([]MonthInputs, len(c))
		rng := rand.New(rand.NewSource(rapid.Int64().Draw(t, "seed")))
		for i, in := range c {
			in.Events = append([]Event(nil), in.Events...)
			in.Outflows = append([]Outflow(nil), in.Outflows...)
			rng.Shuffle(len(in.Events), func(x, y int) { in.Events[x], in.Events[y] = in.Events[y], in.Events[x] })
			rng.Shuffle(len(in.Outflows), func(x, y int) { in.Outflows[x], in.Outflows[y] = in.Outflows[y], in.Outflows[x] })
			shuffled[i] = in
		}
		sh := run(shuffled)
		for i := range a {
			if a[i].InputHash != b[i].InputHash || a[i].SpendLeft != b[i].SpendLeft {
				t.Fatalf("month %d: recomputing differs", i)
			}
			if a[i].SpendLeft != sh[i].SpendLeft || a[i].Pot != sh[i].Pot || a[i].SpendBase != sh[i].SpendBase || len(a[i].Entries) != len(sh[i].Entries) {
				t.Fatalf("month %d: input order changed the result", i)
			}
		}
	})
}

func TestA9_Inv13_InputsAreNotModified(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := chain(t)
		before := make([]MonthInputs, len(c))
		for i, in := range c {
			in.Events = append([]Event(nil), in.Events...)
			in.Outflows = append([]Outflow(nil), in.Outflows...)
			before[i] = in
		}
		run(c)
		for i := range c {
			for j := range c[i].Events {
				if c[i].Events[j] != before[i].Events[j] {
					t.Fatal("ComputeMonth reordered or changed the caller's events")
				}
			}
			for j := range c[i].Outflows {
				if c[i].Outflows[j] != before[i].Outflows[j] {
					t.Fatal("ComputeMonth reordered or changed the caller's outflows")
				}
			}
		}
	})
}

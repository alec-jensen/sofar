package money

import (
	"errors"
	"testing"

	"pgregory.net/rapid"
)

func TestS17_PerDayRemainderToLastDay(t *testing.T) {
	for _, tc := range []struct {
		left      Cents
		days      int
		perDay    Cents
		lastExtra Cents
	}{
		{100000, 30, 3333, 10},
		{100000, 1, 100000, 0},
		{99, 4, 24, 3},
		{0, 10, 0, 0},
		{100, 0, 100, 0},  // zero days is treated as one
		{100, -5, 100, 0}, // so are negative days
		{-100, 3, -34, 2}, // overspent: floor, remainder keeps the identity
	} {
		p, r := PerDay(tc.left, tc.days)
		if p != tc.perDay || r != tc.lastExtra {
			t.Errorf("PerDay(%d,%d) = %d,%d want %d,%d", tc.left, tc.days, p, r, tc.perDay, tc.lastExtra)
		}
	}
}

func TestA9_Inv10_PerDayTimesDaysPlusRemainder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		left := Cents(rapid.Int64Range(-1_000_000_000, 1_000_000_000).Draw(t, "left"))
		days := rapid.IntRange(1, 366).Draw(t, "days")
		p, r := PerDay(left, days)
		if p*Cents(days)+r != left {
			t.Fatalf("perDay*days + remainder = %d, want %d", p*Cents(days)+r, left)
		}
		if r < 0 || r >= Cents(days) {
			t.Fatalf("remainder %d out of [0,%d)", r, days)
		}
	})
}

func TestMoney_PctFloors(t *testing.T) {
	for _, tc := range []struct {
		base Cents
		bp   int64
		want Cents
	}{{10000, 2500, 2500}, {999, 5000, 499}, {-999, 5000, -500}, {1, 1, 0}, {1 << 60, 10000, 1 << 60}} {
		if got := Pct(tc.base, tc.bp); got != tc.want {
			t.Errorf("Pct(%d,%d) = %d want %d", tc.base, tc.bp, got, tc.want)
		}
	}
}

func TestMoney_FromDecimalStringExact(t *testing.T) {
	for in, want := range map[string]Cents{
		"-33293.43":             -3329343,
		"0":                     0,
		"12":                    1200,
		"12.5":                  1250,
		".5":                    50,
		"5.":                    500,
		"+7.01":                 701,
		"-0.01":                 -1,
		"1.005":                 101, // floats would give 100
		"-1.005":                -101,
		"2.675":                 268,
		"0.004":                 0,
		"0.005":                 1,
		"12.345":                1235,
		"  42.10 ":              4210,
		"90071992547409.93":     9007199254740993, // beyond float64 integer precision
		"92233720368547758.07":  9223372036854775807,
		"-92233720368547758.08": -9223372036854775808,
	} {
		got, err := FromDecimalString(in)
		if err != nil || got != want {
			t.Errorf("FromDecimalString(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestMoney_FromDecimalStringRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", " ", "abc", "1e3", "0x10", "1,000.00", "--1", "1.2.3", ".", "+", "NaN", "1 2"} {
		if _, err := FromDecimalString(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
	if _, err := FromDecimalString("92233720368547758.08"); !errors.Is(err, ErrRange) {
		t.Errorf("one cent over int64 must be a range error, got %v", err)
	}
	if _, err := FromDecimalString("99999999999999999999999999.99"); !errors.Is(err, ErrRange) {
		t.Errorf("huge values must be a range error, got %v", err)
	}
}

func TestMoney_Helpers(t *testing.T) {
	if Cents(5).Add(3) != 8 || Cents(5).Sub(8) != -3 || Cents(5).Neg() != -5 || Cents(-5).Abs() != 5 || Min(2, 3) != 2 || Max(2, 3) != 3 {
		t.Fatal("helper arithmetic")
	}
	if Cents(-1234).String() != "-12.34" || Cents(5).String() != "0.05" {
		t.Fatalf("String: %s %s", Cents(-1234), Cents(5))
	}
}

func FuzzFromDecimalString(f *testing.F) {
	for _, s := range []string{"-33293.43", "1.005", "", "9" + "9999999999999999999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := FromDecimalString(s)
		if err == nil && c.String() == "" {
			t.Fatal("unreachable")
		}
	})
}

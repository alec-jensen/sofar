// Package money holds integer-cent arithmetic. Budget math never uses floats
// (architecture A4). It imports nothing from sofar.
package money

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Cents is an amount in minor units. Negative means outflow where a sign matters.
type Cents int64

// Add returns a+b.
func (a Cents) Add(b Cents) Cents { return a + b }

// Sub returns a-b.
func (a Cents) Sub(b Cents) Cents { return a - b }

// Neg returns -a.
func (a Cents) Neg() Cents { return -a }

// Abs returns |a|.
func (a Cents) Abs() Cents {
	if a < 0 {
		return -a
	}
	return a
}

// Min returns the smaller of a and b.
func Min(a, b Cents) Cents {
	if a < b {
		return a
	}
	return b
}

// Max returns the larger of a and b.
func Max(a, b Cents) Cents {
	if a > b {
		return a
	}
	return b
}

// String is for debugging and tests only, never UI copy.
func (a Cents) String() string {
	sign := ""
	if a < 0 {
		sign = "-"
		a = -a
	}
	return fmt.Sprintf("%s%d.%02d", sign, a/100, a%100)
}

// PerDay splits what is left across the days remaining (including today) by
// floor division. The remainder goes on the last day (S17). A non-positive
// daysLeft is treated as 1.
func PerDay(left Cents, daysLeft int) (perDay, lastDayExtra Cents) {
	if daysLeft <= 0 {
		daysLeft = 1
	}
	n := Cents(daysLeft)
	perDay = floorDiv(left, n)
	return perDay, left - perDay*n
}

func floorDiv(a, b Cents) Cents {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Pct returns floor(base * basisPoints / 10000), using 128-bit math so large
// bases cannot overflow.
func Pct(base Cents, basisPoints int64) Cents {
	n := new(big.Int).Mul(big.NewInt(int64(base)), big.NewInt(basisPoints))
	q := new(big.Int).Div(n, big.NewInt(10000)) // Euclidean division: the floor for a positive divisor
	return Cents(q.Int64())
}

// ErrRange means an amount does not fit in int64 cents.
var ErrRange = errors.New("amount out of range")

// FromDecimalString parses a provider amount such as "-33293.43" exactly, never
// through floats. More than two decimal places are rounded half away from zero
// ("1.005" is 101 cents). Signs, a leading or trailing decimal point and long
// digit strings are fine; anything else is an error.
func FromDecimalString(s string) (Cents, error) {
	s = strings.TrimSpace(s)
	if !validDecimal(s) {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	r.Mul(r, big.NewRat(100, 1))
	// round half away from zero
	num, den := r.Num(), r.Denom()
	neg := num.Sign() < 0
	q, rem := new(big.Int).QuoRem(new(big.Int).Abs(num), den, new(big.Int))
	if rem.Lsh(rem, 1).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if neg {
		q.Neg(q)
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("%w: %q", ErrRange, s)
	}
	return Cents(q.Int64()), nil
}

func validDecimal(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	digits, dots := 0, 0
	for ; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digits++
		case s[i] == '.':
			dots++
		default:
			return false
		}
	}
	return digits > 0 && dots <= 1
}

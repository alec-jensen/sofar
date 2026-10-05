// Package money holds integer-cent arithmetic. Budget math never uses floats
// (architecture A4). M1 fills this out; M0 only needs the type.
package money

// Cents is an amount in minor units. Negative means outflow where a sign matters.
type Cents int64

package store

import (
	"database/sql/driver"
	"strconv"
	"strings"
)

// TextArray passes a []string as a Postgres text[] parameter. Use it with an
// explicit cast: `$1::text[]`. To read arrays back, select `to_json(col)` and
// unmarshal into []string.
type TextArray []string

// Value renders a Postgres array literal with every element quoted and escaped.
func (a TextArray) Value() (driver.Value, error) {
	parts := make([]string, len(a))
	for i, v := range a {
		parts[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

// Int64Array passes a []int64 as a Postgres bigint[] parameter (`$1::bigint[]`).
type Int64Array []int64

// Value renders a Postgres array literal.
func (a Int64Array) Value() (driver.Value, error) {
	parts := make([]string, len(a))
	for i, v := range a {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

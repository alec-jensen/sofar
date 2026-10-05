package events

import (
	"database/sql/driver"
	"strconv"
	"strings"
)

// int64Array and stringArray render Postgres array literals so the pgx stdlib
// driver can pass them as one parameter.
type int64Array []int64

func (a int64Array) Value() (driver.Value, error) {
	parts := make([]string, len(a))
	for i, v := range a {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

type stringArray []string

func (a stringArray) Value() (driver.Value, error) {
	parts := make([]string, len(a))
	for i, v := range a {
		parts[i] = strconv.Quote(v)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

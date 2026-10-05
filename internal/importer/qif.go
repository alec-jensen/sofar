package importer

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
)

// ParseQIF reads a Quicken Interchange Format file. Dates are read as US
// month/day/year (with the apostrophe year form, 1/2'26) unless dayFirst is set.
func ParseQIF(r io.Reader, dayFirst bool) (Parsed, error) {
	var out Parsed
	sc := bufio.NewScanner(io.LimitReader(r, MaxBytes+1))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var rec struct {
		date, amount, payee, memo, num string
		any                            bool
	}
	line, recStart, size := 0, 1, 0
	flush := func() {
		if !rec.any {
			return
		}
		defer func() {
			rec = struct {
				date, amount, payee, memo, num string
				any                            bool
			}{}
			recStart = line + 1
		}()
		if len(out.Rows) >= MaxRows {
			return
		}
		date, err := qifDate(rec.date, dayFirst)
		if err != nil {
			out.Skipped = append(out.Skipped, Skipped{recStart, err.Error()})
			return
		}
		amount, err := parseAmount(rec.amount)
		if err != nil {
			out.Skipped = append(out.Skipped, Skipped{recStart, err.Error()})
			return
		}
		desc := clip(rec.payee)
		if desc == "" {
			desc = clip(rec.memo)
		}
		out.Rows = append(out.Rows, Row{ProviderID: clip(rec.num), Date: date, Amount: amount, Description: desc, Payee: clip(rec.payee), Memo: clip(rec.memo)})
	}
	sawStructure := false
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		size += len(text) + 1
		if size > MaxBytes {
			return Parsed{}, ErrTooLarge
		}
		if text == "" {
			continue
		}
		code, val := text[0], strings.TrimSpace(text[1:])
		switch code {
		case '!':
			sawStructure = true
		case 'D':
			rec.date, rec.any, sawStructure = val, true, true
		case 'T', 'U':
			if rec.amount == "" {
				rec.amount = val
			}
			rec.any = true
		case 'P':
			rec.payee, rec.any = val, true
		case 'M':
			rec.memo, rec.any = val, true
		case 'N':
			rec.num, rec.any = val, true
		case '^':
			flush()
		}
	}
	if err := sc.Err(); err != nil {
		return Parsed{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	flush()
	if !sawStructure {
		return Parsed{}, fmt.Errorf("%w: no QIF records found", ErrFormat)
	}
	return out, nil
}

func qifDate(s string, dayFirst bool) (civil.Date, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	s = strings.Replace(s, "'", "/", 1) // 1/2'26 means 1/2/2026
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' || r == '.' })
	if len(parts) != 3 {
		return civil.Date{}, fmt.Errorf("unreadable date %q", clip(s))
	}
	a, b, y := parts[0], parts[1], parts[2]
	if dayFirst {
		a, b = b, a
	}
	if len(y) == 2 {
		y = "20" + y
	}
	t, err := time.Parse("1/2/2006", a+"/"+b+"/"+y)
	if err != nil {
		return civil.Date{}, fmt.Errorf("unreadable date %q", clip(s))
	}
	return civil.DateOf(t), nil
}

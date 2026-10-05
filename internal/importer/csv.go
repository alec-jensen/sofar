package importer

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

// Mapping tells the CSV reader which column is which. Column values are header
// names (case-insensitive) or, when HasHeader is false, 1-based numbers like "3".
type Mapping struct {
	HasHeader   bool   `json:"has_header"`
	Delimiter   string `json:"delimiter,omitempty"` // default ","
	Date        string `json:"date"`
	DateFormat  string `json:"date_format,omitempty"` // Go layout; empty tries common ones
	Amount      string `json:"amount,omitempty"`      // signed amount column
	Debit       string `json:"debit,omitempty"`       // use Debit and Credit instead of Amount
	Credit      string `json:"credit,omitempty"`
	Description string `json:"description"`
	Payee       string `json:"payee,omitempty"`
	Memo        string `json:"memo,omitempty"`
	ID          string `json:"id,omitempty"`
	Invert      bool   `json:"invert,omitempty"` // credit card exports often show spending as positive
}

var dateLayouts = []string{"2006-01-02", "01/02/2006", "1/2/2006", "01/02/06", "1/2/06", "2006/01/02", "Jan 2, 2006", "January 2, 2006", "02-Jan-2006", "2-Jan-2006", "01-02-2006", "20060102"}

func parseDate(s, layout string) (civil.Date, error) {
	s = strings.TrimSpace(s)
	layouts := dateLayouts
	if layout != "" {
		layouts = []string{layout}
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return civil.DateOf(t), nil
		}
	}
	return civil.Date{}, fmt.Errorf("unreadable date %q", clip(s))
}

// parseAmount reads "1,234.56", "$12.00", "(12.34)", "12.34-", "-12.34" and "+5".
func parseAmount(s string) (money.Cents, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	if strings.HasSuffix(s, "-") {
		neg, s = true, s[:len(s)-1]
	}
	s = strings.NewReplacer("$", "", "€", "", "£", "", ",", "", " ", "", " ", "").Replace(s)
	if strings.HasPrefix(s, "-") {
		neg, s = !neg, s[1:]
	}
	s = strings.TrimPrefix(s, "+")
	c, err := money.FromDecimalString(s)
	if err != nil {
		return 0, fmt.Errorf("unreadable amount %q", clip(s))
	}
	if neg {
		c = -c
	}
	return c, nil
}

func decodeText(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	return string(data)
}

// ParseCSV reads a CSV export with the given column mapping.
func ParseCSV(r io.Reader, m Mapping) (Parsed, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return Parsed{}, err
	}
	if len(data) > MaxBytes {
		return Parsed{}, ErrTooLarge
	}
	cr := csv.NewReader(strings.NewReader(decodeText(data)))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	if m.Delimiter != "" {
		cr.Comma = []rune(m.Delimiter)[0]
	}
	records, err := cr.ReadAll()
	if err != nil {
		return Parsed{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	var header []string
	start := 0
	if m.HasHeader && len(records) > 0 {
		header, start = records[0], 1
	}
	col := func(name string) (int, error) {
		if name == "" {
			return -1, nil
		}
		for i, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), name) {
				return i, nil
			}
		}
		var n int
		if _, err := fmt.Sscanf(name, "%d", &n); err == nil && n >= 1 {
			return n - 1, nil
		}
		return -1, fmt.Errorf("%w: no column named %q", ErrFormat, name)
	}
	idx := map[string]int{}
	for key, name := range map[string]string{"date": m.Date, "amount": m.Amount, "debit": m.Debit, "credit": m.Credit, "desc": m.Description, "payee": m.Payee, "memo": m.Memo, "id": m.ID} {
		i, err := col(name)
		if err != nil {
			return Parsed{}, err
		}
		idx[key] = i
	}
	if idx["date"] < 0 || idx["desc"] < 0 || (idx["amount"] < 0 && idx["debit"] < 0 && idx["credit"] < 0) {
		return Parsed{}, fmt.Errorf("%w: a date, a description and an amount (or debit and credit) column are required", ErrFormat)
	}
	get := func(rec []string, key string) string {
		if i := idx[key]; i >= 0 && i < len(rec) {
			return rec[i]
		}
		return ""
	}
	var out Parsed
	for n := start; n < len(records); n++ {
		rec := records[n]
		line := n + 1
		if len(out.Rows) >= MaxRows {
			out.Skipped = append(out.Skipped, Skipped{line, "row limit reached"})
			break
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		date, err := parseDate(get(rec, "date"), m.DateFormat)
		if err != nil {
			out.Skipped = append(out.Skipped, Skipped{line, err.Error()})
			continue
		}
		var amount money.Cents
		if idx["amount"] >= 0 {
			amount, err = parseAmount(get(rec, "amount"))
		} else {
			var debit, credit money.Cents
			if d := strings.TrimSpace(get(rec, "debit")); d != "" {
				if debit, err = parseAmount(d); err == nil {
					debit = debit.Abs()
				}
			}
			if c := strings.TrimSpace(get(rec, "credit")); c != "" && err == nil {
				if credit, err = parseAmount(c); err == nil {
					credit = credit.Abs()
				}
			}
			amount = credit - debit
		}
		if err != nil {
			out.Skipped = append(out.Skipped, Skipped{line, err.Error()})
			continue
		}
		if m.Invert {
			amount = -amount
		}
		desc := clip(get(rec, "desc"))
		if desc == "" {
			desc = clip(get(rec, "payee"))
		}
		out.Rows = append(out.Rows, Row{ProviderID: clip(get(rec, "id")), Date: date, Amount: amount, Description: desc, Payee: clip(get(rec, "payee")), Memo: clip(get(rec, "memo"))})
	}
	return out, nil
}

// GuessMapping inspects a header row and proposes a mapping, for the file picker.
func GuessMapping(header []string) Mapping {
	m := Mapping{HasHeader: true}
	find := func(words ...string) string {
		for _, h := range header {
			low := strings.ToLower(h)
			for _, w := range words {
				if strings.Contains(low, w) {
					return strings.TrimSpace(h)
				}
			}
		}
		return ""
	}
	m.Date = find("posted", "post date", "date")
	m.Description = find("description", "memo", "name", "payee", "details")
	m.Amount = find("amount")
	if m.Amount == "" {
		m.Debit, m.Credit = find("debit", "withdrawal"), find("credit", "deposit")
	}
	m.ID = find("transaction id", "reference", "fitid")
	return m
}

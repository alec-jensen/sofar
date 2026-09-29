package budget

import (
	"math"
	"regexp"
	"strings"
	"time"
)

type Transaction struct {
	ID                     string  `json:"id"`
	AccountID              string  `json:"accountId"`
	Date                   string  `json:"date"`
	Amount                 int64   `json:"amount"`
	Merchant               string  `json:"merchant"`
	Category               *string `json:"category"`
	Status                 string  `json:"status"`
	Direction              string  `json:"direction"`
	IncomeStream           string  `json:"incomeStream,omitempty"`
	Suggested              string  `json:"suggested,omitempty"`
	SubcategoryID          string  `json:"subcategoryId,omitempty"`
	SuggestedSubcategoryID string  `json:"suggestedSubcategoryId,omitempty"`
	RecurringID            string  `json:"recurringId,omitempty"`
	BankPending            bool    `json:"bankPending,omitempty"`
	Note                   string  `json:"note,omitempty"`
	Ignored                bool    `json:"ignored,omitempty"`
	IgnoreReason           string  `json:"ignoreReason,omitempty"`
	Splits                 []Split `json:"splits,omitempty"`
}
type Split struct {
	Category      string `json:"category"`
	SubcategoryID string `json:"subcategoryId,omitempty"`
	Amount        int64  `json:"amount"`
}
type Recurring struct {
	ID           string  `json:"id"`
	Merchant     string  `json:"merchant"`
	Amount       int64   `json:"amount"`
	Tolerance    float64 `json:"tolerance"`
	Cadence      string  `json:"cadence"`
	Type         string  `json:"type"`
	IncomeStream string  `json:"incomeStream,omitempty"`
	Category     string  `json:"category"`
	Confirmed    bool    `json:"confirmed"`
	Dismissed    bool    `json:"dismissed,omitempty"`
	Threshold    int     `json:"threshold"`
	NextDate     string  `json:"nextDate"`
	Mismatch     bool    `json:"mismatch,omitempty"`
}
type Link struct {
	ID        string `json:"id"`
	ExpenseID string `json:"expenseId"`
	CreditID  string `json:"creditId"`
	Amount    int64  `json:"amount"`
}
type Baseline struct {
	Salary       int64 `json:"salary"`
	SelfEmployed int64 `json:"selfEmployed"`
	Bills        int64 `json:"bills"`
	Safe         int64 `json:"safe"`
}

func Calculate(ts []Transaction, rs []Recurring, links []Link, monthly int64, now time.Time) Baseline {
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	start := end.AddDate(0, -3, 0)
	var b Baseline
	linked := map[string]bool{}
	for _, l := range links {
		linked[l.CreditID] = true
	}
	for _, t := range ts {
		d, e := time.ParseInLocation("2006-01-02", t.Date, now.Location())
		if e != nil || t.Status != "confirmed" || t.BankPending || t.Direction != "in" || linked[t.ID] || d.Before(start) || !d.Before(end) {
			continue
		}
		switch t.IncomeStream {
		case "salary":
			b.Salary += t.Amount
		case "self-employed":
			b.SelfEmployed += t.Amount
		}
	}
	b.Salary = int64(math.Round(float64(b.Salary) / 3))
	b.SelfEmployed = int64(math.Round(float64(b.SelfEmployed) / 3))
	for _, r := range rs {
		if r.Confirmed && !r.Dismissed && r.Type == "bill" && r.Category == "expenses" {
			b.Bills += Monthly(r.Amount, r.Cadence)
		}
	}
	b.Safe = b.Salary + b.SelfEmployed - b.Bills - monthly
	return b
}
func Monthly(n int64, cadence string) int64 {
	factor := map[string]float64{"weekly": 52.0 / 12, "biweekly": 26.0 / 12, "monthly": 1, "annual": 1.0 / 12}[cadence]
	return int64(math.Round(float64(n) * factor))
}

var nonLetters = regexp.MustCompile(`[^a-z ]`)
var spaces = regexp.MustCompile(`\s+`)

func Normalize(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(nonLetters.ReplaceAllString(strings.ReplaceAll(strings.ToLower(s), ".com", ""), " "), " "))
}
func Matches(a, b string) bool {
	a = Normalize(a)
	b = Normalize(b)
	if len(a) < 3 || len(b) < 3 {
		return a == b && a != ""
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}
func Cadence(dates []time.Time) string {
	if len(dates) < 2 {
		return ""
	}
	var days float64
	for i := 1; i < len(dates); i++ {
		days += dates[i].Sub(dates[i-1]).Hours() / 24
	}
	avg := days / float64(len(dates)-1)
	for _, c := range []struct {
		name            string
		days, tolerance float64
	}{{"weekly", 7, 2}, {"biweekly", 14, 3}, {"monthly", 30.44, 5}, {"annual", 365.25, 20}} {
		if math.Abs(avg-c.days) > c.tolerance {
			continue
		}
		valid := true
		for i := 1; i < len(dates); i++ {
			if math.Abs(dates[i].Sub(dates[i-1]).Hours()/24-c.days) > c.tolerance {
				valid = false
			}
		}
		if valid {
			return c.name
		}
	}
	return ""
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/rs/zerolog/log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type account struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Institution string `json:"institution"`
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Mask        string `json:"mask"`
	Balance     int64  `json:"balance"`
	SyncedAt    string `json:"syncedAt"`
}
type goal struct {
	Name    string `json:"name"`
	Target  int64  `json:"target"`
	Monthly int64  `json:"monthly"`
	Saved   int64  `json:"saved"`
}
type rule struct {
	Pattern  string `json:"pattern"`
	Category string `json:"category"`
}
type state struct {
	Transactions []budget.Transaction `json:"transactions"`
	Accounts     []account            `json:"accounts"`
	Recurring    []budget.Recurring   `json:"recurring"`
	Rules        []rule               `json:"rules"`
	Links        []budget.Link        `json:"links"`
	Goal         goal                 `json:"goal"`
	Categories   map[string]string    `json:"categories"`
	Threshold    int                  `json:"threshold"`
	LastSync     string               `json:"lastSync"`
	Demo         bool                 `json:"demo"`
	Budget       budget.Baseline      `json:"budget"`
}
type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readState(ctx context.Context, q querier) (state, error) {
	s := state{Transactions: []budget.Transaction{}, Accounts: []account{}, Recurring: []budget.Recurring{}, Rules: []rule{}, Links: []budget.Link{}, Categories: map[string]string{}, Threshold: 3}
	rows, e := q.QueryContext(ctx, "SELECT id,name FROM categories")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var id, name string
		if e = rows.Scan(&id, &name); e != nil {
			rows.Close()
			return s, e
		}
		s.Categories[id] = name
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	rows, e = q.QueryContext(ctx, "SELECT id,display_name,institution_name,account_type,account_subtype,mask,balance,COALESCE(to_char(last_synced_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),'') FROM accounts ORDER BY account_type,id")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var a account
		if e = rows.Scan(&a.ID, &a.Name, &a.Institution, &a.Type, &a.Subtype, &a.Mask, &a.Balance, &a.SyncedAt); e != nil {
			rows.Close()
			return s, e
		}
		s.Accounts = append(s.Accounts, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	rows, e = q.QueryContext(ctx, "SELECT merchant_pattern,category_id FROM rules ORDER BY length(merchant_pattern) DESC")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var r rule
		if e = rows.Scan(&r.Pattern, &r.Category); e != nil {
			rows.Close()
			return s, e
		}
		s.Rules = append(s.Rules, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	rows, e = q.QueryContext(ctx, "SELECT id,account_id,date::text,amount,raw_merchant,category_id,review_status,direction,COALESCE(income_stream,''),COALESCE(recurring_group_id,''),bank_pending FROM transactions ORDER BY date DESC,id")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var t budget.Transaction
		if e = rows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Amount, &t.Merchant, &t.Category, &t.Status, &t.Direction, &t.IncomeStream, &t.RecurringID, &t.BankPending); e != nil {
			rows.Close()
			return s, e
		}
		t.Suggested = "spending"
		for _, r := range s.Rules {
			if budget.Matches(t.Merchant, r.Pattern) {
				t.Suggested = r.Category
				break
			}
		}
		s.Transactions = append(s.Transactions, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	rows, e = q.QueryContext(ctx, "SELECT id,merchant_pattern,expected_amount,amount_tolerance_pct,cadence,type,COALESCE(income_stream,''),COALESCE(category_id,'expenses'),confirmed,dismissed,occurrence_threshold_at_creation,next_date::text,mismatch FROM recurring_groups ORDER BY next_date,id")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var r budget.Recurring
		if e = rows.Scan(&r.ID, &r.Merchant, &r.Amount, &r.Tolerance, &r.Cadence, &r.Type, &r.IncomeStream, &r.Category, &r.Confirmed, &r.Dismissed, &r.Threshold, &r.NextDate, &r.Mismatch); e != nil {
			rows.Close()
			return s, e
		}
		s.Recurring = append(s.Recurring, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	rows, e = q.QueryContext(ctx, "SELECT id,expense_transaction_id,reimbursement_transaction_id,amount_netted FROM reimbursement_links")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var l budget.Link
		if e = rows.Scan(&l.ID, &l.ExpenseID, &l.CreditID, &l.Amount); e != nil {
			rows.Close()
			return s, e
		}
		s.Links = append(s.Links, l)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	if e = q.QueryRowContext(ctx, "SELECT name,COALESCE(target_amount,0),target_monthly_contribution FROM savings_goals WHERE is_active=true ORDER BY id LIMIT 1").Scan(&s.Goal.Name, &s.Goal.Target, &s.Goal.Monthly); e != nil {
		return s, e
	}
	for _, t := range s.Transactions {
		if t.Status == "confirmed" && t.Category != nil && *t.Category == "savings" && t.Direction == "out" && !t.BankPending {
			s.Goal.Saved += t.Amount
			for _, l := range s.Links {
				if l.ExpenseID == t.ID {
					s.Goal.Saved -= l.Amount
				}
			}
		}
	}
	var threshold string
	if e = q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='recurring_occurrence_threshold'").Scan(&threshold); e != nil {
		return s, e
	}
	s.Threshold, _ = strconv.Atoi(threshold)
	if e = q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='last_sync'").Scan(&s.LastSync); e != nil {
		return s, e
	}
	s.Budget = budget.Calculate(s.Transactions, s.Recurring, s.Links, s.Goal.Monthly, time.Now())
	return s, nil
}
func (s *server) stateHandler(w http.ResponseWriter, r *http.Request) {
	tx, e := s.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		fail(w, 500, "Could not load your workspace.")
		return
	}
	defer tx.Rollback()
	st, e := readState(r.Context(), tx)
	if e != nil {
		log.Error().Err(e).Msg("load state")
		fail(w, 500, "Could not load your workspace.")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "Could not load your workspace.")
		return
	}
	respond(w, st)
}

type action struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	TransactionID string            `json:"transactionId"`
	Category      string            `json:"category"`
	IncomeStream  string            `json:"incomeStream"`
	ExpenseID     string            `json:"expenseId"`
	CreditID      string            `json:"creditId"`
	Amount        int64             `json:"amount"`
	LinkID        string            `json:"linkId"`
	RecurringID   string            `json:"recurringId"`
	Tolerance     float64           `json:"tolerance"`
	Goal          *goal             `json:"goal"`
	Threshold     int               `json:"threshold"`
	Categories    map[string]string `json:"categories"`
}

func validCategory(c string) bool { return c == "expenses" || c == "spending" || c == "savings" }
func execOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	r, e := tx.ExecContext(ctx, query, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return errors.New("item no longer available")
	}
	return nil
}
func apply(ctx context.Context, tx *sql.Tx, a action) error {
	switch a.Type {
	case "review":
		if !validCategory(a.Category) {
			return errors.New("choose a valid category")
		}
		var merchant, direction string
		var pending bool
		if e := tx.QueryRowContext(ctx, "SELECT raw_merchant,direction,bank_pending FROM transactions WHERE id=$1", a.TransactionID).Scan(&merchant, &direction, &pending); e != nil {
			return errors.New("transaction not found")
		}
		if pending {
			return errors.New("wait for this bank transaction to post")
		}
		if a.IncomeStream != "" && a.IncomeStream != "salary" && a.IncomeStream != "self-employed" && a.IncomeStream != "transfer" {
			return errors.New("choose a valid income type")
		}
		var linked bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM reimbursement_links WHERE reimbursement_transaction_id=$1)", a.TransactionID).Scan(&linked); e != nil {
			return e
		}
		if direction == "in" && a.IncomeStream == "" && !linked {
			return errors.New("classify incoming money as salary, self-employed, transfer, or a repayment")
		}
		if direction == "out" {
			a.IncomeStream = ""
		}
		if e := execOne(ctx, tx, "UPDATE transactions SET category_id=$1,review_status='confirmed',income_stream=NULLIF($2,'') WHERE id=$3", a.Category, a.IncomeStream, a.TransactionID); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO rules VALUES($1,$2) ON CONFLICT(merchant_pattern) DO UPDATE SET category_id=EXCLUDED.category_id", budget.Normalize(merchant), a.Category)
		return e
	case "reimburse":
		var expense, credit, used int64
		var cat sql.NullString
		var expenseDate, creditDate time.Time
		if e := tx.QueryRowContext(ctx, "SELECT amount,category_id,date FROM transactions WHERE id=$1 AND direction='out' AND review_status='confirmed' AND bank_pending=false FOR UPDATE", a.ExpenseID).Scan(&expense, &cat, &expenseDate); e != nil {
			return errors.New("choose a confirmed expense")
		}
		if e := tx.QueryRowContext(ctx, "SELECT amount,date FROM transactions WHERE id=$1 AND direction='in' AND bank_pending=false FOR UPDATE", a.CreditID).Scan(&credit, &creditDate); e != nil {
			return errors.New("choose an incoming credit")
		}
		if creditDate.Before(expenseDate) {
			return errors.New("the expense must precede the repayment")
		}
		if e := tx.QueryRowContext(ctx, "SELECT COALESCE(sum(amount_netted),0) FROM reimbursement_links WHERE expense_transaction_id=$1", a.ExpenseID).Scan(&used); e != nil {
			return e
		}
		if a.Amount <= 0 || a.Amount > credit || a.Amount > expense-used {
			return errors.New("repayment exceeds the available credit or remaining expense")
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO reimbursement_links(id,expense_transaction_id,reimbursement_transaction_id,amount_netted) VALUES($1,$2,$3,$4)", a.ID, a.ExpenseID, a.CreditID, a.Amount); e != nil {
			return errors.New("this credit is already linked")
		}
		return execOne(ctx, tx, "UPDATE transactions SET review_status='confirmed',category_id=$1,income_stream=NULL WHERE id=$2", cat, a.CreditID)
	case "unlink":
		var credit string
		if e := tx.QueryRowContext(ctx, "DELETE FROM reimbursement_links WHERE id=$1 RETURNING reimbursement_transaction_id", a.LinkID).Scan(&credit); e != nil {
			return errors.New("repayment link not found")
		}
		return execOne(ctx, tx, "UPDATE transactions SET review_status='pending',category_id=NULL,income_stream=NULL WHERE id=$1", credit)
	case "recurring":
		if !validCategory(a.Category) || a.Tolerance < 0 || a.Tolerance > 100 || a.Amount < 0 || a.Amount > 10000000000 {
			return errors.New("choose a category and tolerance between 0 and 100")
		}
		var kind string
		if e := tx.QueryRowContext(ctx, "SELECT type FROM recurring_groups WHERE id=$1", a.RecurringID).Scan(&kind); e != nil {
			return errors.New("recurring item not found")
		}
		if kind == "income" && a.IncomeStream != "salary" && a.IncomeStream != "self-employed" {
			return errors.New("choose a separate income stream")
		}
		if kind == "bill" {
			a.IncomeStream = ""
		}
		return execOne(ctx, tx, "UPDATE recurring_groups SET confirmed=true,dismissed=false,category_id=$1,amount_tolerance_pct=$2,income_stream=NULLIF($3,''),mismatch=false,expected_amount=CASE WHEN $5>0 THEN $5 ELSE expected_amount END WHERE id=$4", a.Category, a.Tolerance, a.IncomeStream, a.RecurringID, a.Amount)
	case "dismiss-recurring":
		return execOne(ctx, tx, "UPDATE recurring_groups SET dismissed=true,mismatch=false WHERE id=$1", a.RecurringID)
	case "restore-recurring":
		return execOne(ctx, tx, "UPDATE recurring_groups SET dismissed=false WHERE id=$1", a.RecurringID)
	case "goal":
		if a.Goal == nil || strings.TrimSpace(a.Goal.Name) == "" || len(a.Goal.Name) > 80 || a.Goal.Monthly < 0 || a.Goal.Monthly > 10000000000 || a.Goal.Target <= 0 || a.Goal.Target > 10000000000 {
			return errors.New("enter a name, positive target, and nonnegative contribution")
		}
		return execOne(ctx, tx, "UPDATE savings_goals SET name=$1,target_amount=$2,target_monthly_contribution=$3 WHERE id=(SELECT id FROM savings_goals WHERE is_active=true ORDER BY id LIMIT 1)", a.Goal.Name, a.Goal.Target, a.Goal.Monthly)
	case "settings":
		if a.Threshold < 2 || a.Threshold > 24 {
			return errors.New("use between 2 and 24 occurrences")
		}
		if len(a.Categories) != 3 {
			return errors.New("all three categories are required")
		}
		for _, c := range []string{"expenses", "spending", "savings"} {
			name := strings.TrimSpace(a.Categories[c])
			if len(name) < 1 || len(name) > 30 {
				return errors.New("category names must be between 1 and 30 characters")
			}
			if e := execOne(ctx, tx, "UPDATE categories SET name=$1 WHERE id=$2", name, c); e != nil {
				return e
			}
		}
		return execOne(ctx, tx, "UPDATE settings SET value=$1 WHERE key='recurring_occurrence_threshold'", strconv.Itoa(a.Threshold))
	default:
		return errors.New("unknown action")
	}
}
func (s *server) actionHandler(w http.ResponseWriter, r *http.Request) {
	var a action
	if decode(r, &a) != nil || len(a.ID) < 16 || len(a.ID) > 100 {
		fail(w, 400, "Invalid action.")
		return
	}
	ctx := r.Context()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		fail(w, 500, "Could not save changes.")
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(731891)"); e != nil {
		fail(w, 500, "Could not save changes.")
		return
	}
	var exists bool
	if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM applied_actions WHERE id=$1)", a.ID).Scan(&exists); e != nil {
		fail(w, 500, "Could not save changes.")
		return
	}
	if !exists {
		if e = apply(ctx, tx, a); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO applied_actions(id) VALUES($1)", a.ID); e != nil {
			fail(w, 500, "Could not save changes.")
			return
		}
	}
	st, e := readState(ctx, tx)
	if e != nil {
		fail(w, 500, "Could not load updated workspace.")
		return
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	if _, e = tx.ExecContext(ctx, "INSERT INTO budget_periods(id,period_start,period_end,salary_baseline_amount,self_employed_baseline_amount) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET salary_baseline_amount=$4,self_employed_baseline_amount=$5,computed_at=now()", fmt.Sprintf("%d-%02d", now.Year(), now.Month()), start, start.AddDate(0, 1, -1), st.Budget.Salary, st.Budget.SelfEmployed); e != nil {
		fail(w, 500, "Could not save budget calculation.")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "Could not save changes.")
		return
	}
	respond(w, st)
}

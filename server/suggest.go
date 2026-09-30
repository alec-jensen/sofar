package main

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
)

// suggest fills each transaction's suggested treatment. Your own rules decide
// first; then money moving between your accounts is recognized by matching
// amounts; then earlier choices for the same payer; then sofar's merchant
// directory. Nothing here confirms a transaction.
func suggest(st *state) {
	accounts := map[string]account{}
	hasCredit := false
	for _, a := range st.Accounts {
		accounts[a.ID] = a
		if a.Type == "credit" {
			hasCredit = true
		}
	}
	subs := map[string]string{}
	for _, c := range st.Subcategories {
		subs[c.ID] = c.Group
	}
	linkedCredit := map[string]bool{}
	for _, l := range st.Links {
		linkedCredit[l.CreditID] = true
	}
	pair := pairTransfers(st.Transactions, accounts)
	history := map[string]string{}
	for _, t := range st.Transactions {
		if t.Direction == "in" && t.Status == "confirmed" && t.IncomeStream != "" && !linkedCredit[t.ID] {
			if _, seen := history[budget.Normalize(t.Merchant)]; !seen {
				history[budget.Normalize(t.Merchant)] = t.IncomeStream
			}
		}
	}
	type lookup struct{ sub, group string }
	cache := map[string]lookup{}
	directory := func(name string) lookup {
		if v, ok := cache[name]; ok {
			return v
		}
		v := lookup{}
		if sub, _, ok := budget.LookupMerchant(name); ok {
			v = lookup{sub, budget.GroupOf(sub)}
		}
		cache[name] = v
		return v
	}
	for i := range st.Transactions {
		t := &st.Transactions[i]
		t.Suggested, t.SuggestedSubcategoryID, t.SuggestedIncomeStream, t.SuggestedIgnore, t.SuggestionNote = "spending", "", "", "", ""
		other, paired := pair[t.ID]
		var otherAccount account
		if paired {
			otherAccount = accounts[other.AccountID]
		}
		if t.Direction == "in" {
			switch {
			case paired:
				t.SuggestedIncomeStream, t.SuggestionNote = "transfer", "moved from "+strings.ToLower(accounts[other.AccountID].Name)
			case history[budget.Normalize(t.Merchant)] != "":
				t.SuggestedIncomeStream = history[budget.Normalize(t.Merchant)]
			default:
				t.SuggestedIncomeStream = budget.LookupIncome(t.Merchant)
			}
			if t.SuggestedIncomeStream == "other" && t.SuggestionNote == "" {
				t.SuggestionNote = "interest, refunds, and gifts don’t count toward your paycheck average"
			}
			continue
		}
		ruleGroup, ruleSub := "", ""
		for _, r := range st.Rules {
			if r.Enabled && budget.Matches(t.Merchant, r.Pattern) {
				ruleGroup, ruleSub = r.Category, r.SubcategoryID
				break
			}
		}
		found := directory(t.Merchant)
		switch {
		case ruleGroup != "":
			// A rule is your decision, including "just the group": the directory never overrides it.
			t.Suggested, t.SuggestedSubcategoryID = ruleGroup, ruleSub
		case paired && (otherAccount.Type == "investment" || otherAccount.Subtype == "savings"):
			t.Suggested, t.SuggestionNote = "savings", "moved to "+strings.ToLower(otherAccount.Name)
			t.SuggestedSubcategoryID = "savings-account"
			if otherAccount.Type == "investment" {
				t.SuggestedSubcategoryID = "investing"
			}
		case paired && otherAccount.Type == "credit":
			t.SuggestedIgnore, t.SuggestionNote = "card payment", "payment to "+strings.ToLower(otherAccount.Name)+"; the purchases already count"
		case paired:
			t.SuggestedIgnore, t.SuggestionNote = "transfer", "moved to "+strings.ToLower(otherAccount.Name)
		case hasCredit && budget.IsCardPayment(t.Merchant):
			t.SuggestedIgnore, t.SuggestionNote = "card payment", "looks like a credit card payment; the purchases already count"
		case found.group != "":
			t.Suggested, t.SuggestedSubcategoryID = found.group, found.sub
		case budget.IsTransferText(t.Merchant) && (accounts[t.AccountID].Subtype == "savings" || accounts[t.AccountID].Type == "investment"):
			// Money leaving savings is usually headed to your own checking, where it'll be spent and counted.
			t.SuggestedIgnore, t.SuggestionNote = "transfer", "moved out of "+strings.ToLower(accounts[t.AccountID].Name)
		case budget.IsTransferText(t.Merchant):
			t.SuggestionNote = "looks like a transfer. if it went to one of your accounts, ignore it"
		}
		if t.SuggestedSubcategoryID != "" && subs[t.SuggestedSubcategoryID] != t.Suggested {
			t.SuggestedSubcategoryID = ""
		}
	}
}

// pairTransfers matches money leaving one of your accounts with the same amount
// arriving in another within four days, closest dates first.
func pairTransfers(ts []budget.Transaction, accounts map[string]account) map[string]budget.Transaction {
	type candidate struct {
		out, in budget.Transaction
		gap     float64
	}
	incoming := map[int64][]budget.Transaction{}
	for _, t := range ts {
		if t.Direction == "in" && !t.Manual {
			incoming[t.Amount] = append(incoming[t.Amount], t)
		}
	}
	var candidates []candidate
	for _, out := range ts {
		if out.Direction != "out" || out.Manual || out.Amount == 0 {
			continue
		}
		od, e := time.Parse("2006-01-02", out.Date)
		if e != nil {
			continue
		}
		for _, in := range incoming[out.Amount] {
			if in.AccountID == out.AccountID || accounts[in.AccountID].ID == "" || accounts[out.AccountID].ID == "" {
				continue
			}
			id, e := time.Parse("2006-01-02", in.Date)
			if e != nil {
				continue
			}
			if gap := math.Abs(id.Sub(od).Hours() / 24); gap <= 4 {
				candidates = append(candidates, candidate{out, in, gap})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].gap < candidates[j].gap })
	pairs := map[string]budget.Transaction{}
	for _, c := range candidates {
		if _, used := pairs[c.out.ID]; used {
			continue
		}
		if _, used := pairs[c.in.ID]; used {
			continue
		}
		pairs[c.out.ID], pairs[c.in.ID] = c.in, c.out
	}
	return pairs
}

// seedDefaults adds sofar's starter categories once. Categories you delete stay deleted.
func seedDefaults(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var done bool
	if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM settings WHERE key='default_categories')").Scan(&done); e != nil || done {
		return e
	}
	for _, c := range budget.Directory.Categories {
		if _, e = tx.ExecContext(ctx, "INSERT INTO subcategories(id,name,group_id,monthly_plan) VALUES($1,$2,$3,0) ON CONFLICT DO NOTHING", c.ID, c.Name, c.Group); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('default_categories','1')"); e != nil {
		return e
	}
	return tx.Commit()
}

// autoCategorize sorts confirmed spending that has no category yet, using
// sofar's merchant directory. A merchant you have a rule for is left alone:
// your rule (even one that says "just the group") is the final word.
func autoCategorize(ctx context.Context, tx *sql.Tx) error {
	st, e := readState(ctx, tx)
	if e != nil {
		return e
	}
	subs := map[string]string{}
	for _, c := range st.Subcategories {
		subs[c.ID] = c.Group
	}
	for _, t := range st.Transactions {
		if t.Status != "confirmed" || t.Direction != "out" || t.Ignored || t.Manual || len(t.Splits) > 0 || t.SubcategoryID != "" {
			continue
		}
		group, sub, ruled := "", "", false
		for _, r := range st.Rules {
			if r.Enabled && budget.Matches(t.Merchant, r.Pattern) {
				group, sub, ruled = r.Category, r.SubcategoryID, true
				break
			}
		}
		if ruled && sub == "" {
			continue
		}
		if !ruled {
			found, _, ok := budget.LookupMerchant(t.Merchant)
			if !ok || subs[found] == "" {
				continue
			}
			group, sub = subs[found], found
		}
		if _, e = tx.ExecContext(ctx, "UPDATE transactions SET category_id=$2,subcategory_id=$3 WHERE id=$1", t.ID, group, sub); e != nil {
			return e
		}
	}
	return nil
}

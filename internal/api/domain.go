package api

import (
	"github.com/alec-jensen/sofar/internal/accounts"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/importer"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/matching"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/recurring"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/transfers"
)

// WithDomain builds the domain services over d.DB and one shared event
// registry, and returns d with them filled in.
func WithDomain(d Deps) Deps {
	reg := events.NewRegistry()
	d.Ledger = ledger.New(d.DB, reg)
	d.Accounts = accounts.New(d.DB, reg)
	d.Categories = categories.New(d.DB, d.Ledger, reg)
	d.Importer = importer.New(d.DB, reg)
	d.Manual = manual.New(d.DB, reg, d.Ledger)
	d.Recompute = recompute.New(d.DB, reg, d.Ledger, d.Cfg.HomeTZ)
	d.Recurring = recurring.New(d.DB, reg, d.Ledger, d.Cfg.HomeTZ)
	d.Recurring.Now = d.Recompute.Now
	d.Budget = budgetcfg.New(d.DB, reg, d.Categories, d.Cfg.HomeTZ)
	d.Budget.Now = d.Recompute.Now
	d.Links = links.New(d.DB, reg)
	d.Counterparties = counterparties.New(d.DB, reg)
	d.Receivables = receivables.New(d.DB, reg, d.Links, d.Counterparties, d.Manual)
	d.Receivables.Now, d.Receivables.Home = d.Recompute.Now, d.Recompute.Home
	d.Reimburse = reimburse.New(d.DB, reg, d.Links, d.Counterparties, d.Receivables)
	d.Reimburse.Now, d.Reimburse.Home = d.Recompute.Now, d.Recompute.Home
	d.Transfers = transfers.New(d.DB, d.Ledger)
	d.Transfers.Now = d.Recompute.Now
	d.Matching = matching.New(d.DB, d.Transfers, d.Reimburse)
	d.Matching.Now = d.Recompute.Now
	d.Categorize = categorize.New(d.DB, d.Ledger, reg)
	d.Categorize.Now = d.Recompute.Now
	d.Events = reg
	return d
}

package api

import (
	"github.com/alec-jensen/sofar/internal/accounts"
	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/importer"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/recompute"
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
	d.Events = reg
	return d
}

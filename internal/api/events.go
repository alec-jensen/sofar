package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/importer"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/recurring"
)

func eventID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	return id, true
}

// undoEvent reverses any user decision by event id, whichever package owns it.
func (s *server) undoEvent(w http.ResponseWriter, r *http.Request) {
	s.reverse(w, r, false)
}

// redoEvent reverses an undo by the undo event's id.
func (s *server) redoEvent(w http.ResponseWriter, r *http.Request) {
	s.reverse(w, r, true)
}

func (s *server) reverse(w http.ResponseWriter, r *http.Request, redo bool) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	target, err := events.Get(r.Context(), s.DB, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	kind := target.Kind
	if redo {
		if kind != events.KindUndo || target.Reverses == nil {
			httpx.Fail(w, http.StatusConflict, "event_state", "Only an undo can be redone.")
			return
		}
		orig, err := events.Get(r.Context(), s.DB, *target.Reverses)
		if err != nil {
			s.fail(w, err)
			return
		}
		kind = orig.Kind
	}
	var out events.Event
	switch kind {
	case importer.KindImportApply:
		if redo {
			out, err = s.Importer.Redo(r.Context(), events.ActorUser, id)
		} else {
			out, err = s.Importer.Undo(r.Context(), events.ActorUser, id)
		}
		if err != nil {
			s.fail(w, err)
			return
		}
	case manual.KindAdd, manual.KindEdit, ledger.KindManualMerge:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Manual.Redo(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Manual.Undo(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case budgetcfg.KindConfigSet:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Budget.RedoVersion(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Budget.UndoVersion(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case recurring.KindCreate, recurring.KindEnd, recurring.KindDismiss:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Recurring.Redo(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Recurring.Undo(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case recompute.KindPlanOverride, recompute.KindBudgetEvent:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = events.Redo(ctx, tx, events.ActorUser, id, "")
			} else {
				out, err = events.Undo(ctx, tx, events.ActorUser, id, "")
			}
			return
		}) {
			return
		}
	case ledger.KindCategoryMerge, ledger.KindCategoryDelete:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Categories.Redo(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Categories.Undo(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case ledger.KindSetCategory, ledger.KindSetNote, ledger.KindSetTags, ledger.KindHide, ledger.KindUnhide, ledger.KindSetMonth,
		ledger.KindSplit, ledger.KindSetFromDiscretionary, ledger.KindSetReviewed, ledger.KindSetType:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Ledger.Redo(ctx, tx, events.ActorUser, id, "")
			} else {
				out, err = s.Ledger.Undo(ctx, tx, events.ActorUser, id, "")
			}
			return
		}) {
			return
		}
	case links.KindCreate, links.KindState:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Links.Redo(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Links.Undo(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case receivables.KindCreate, receivables.KindUpdate, receivables.KindWriteOff:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Receivables.Redo(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Receivables.Undo(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case counterparties.KindCreate, counterparties.KindAlias, counterparties.KindMerge, counterparties.KindSplit, counterparties.KindUpdate:
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if redo {
				out, err = s.Counterparties.Redo(ctx, tx, events.ActorUser, id)
			} else {
				out, err = s.Counterparties.Undo(ctx, tx, events.ActorUser, id)
			}
			return
		}) {
			return
		}
	case categorize.KindApplyPast:
		if redo {
			httpx.Fail(w, http.StatusUnprocessableEntity, "not_undoable", "Apply the change again instead.")
			return
		}
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			out, err = s.Categorize.UndoApplyPast(ctx, tx, events.ActorUser, id)
			return
		}) {
			return
		}
	default:
		httpx.Fail(w, http.StatusUnprocessableEntity, "not_undoable", "That change cannot be undone from here.")
		return
	}
	httpx.Respond(w, map[string]any{"event_id": out.ID})
}

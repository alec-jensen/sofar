package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alec-jensen/sofar/internal/accounts"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/recurring"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/transfers"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// withTx runs fn in a transaction and commits it, turning domain errors into
// the standard error response. It returns false when it already responded.
func (s *server) withTx(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, tx *sql.Tx) error) bool {
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, err)
		return false
	}
	defer tx.Rollback()
	if err = fn(r.Context(), tx); err != nil {
		s.fail(w, err)
		return false
	}
	if err = tx.Commit(); err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

// fail maps a service error to a status and code.
func (s *server) fail(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, ledger.ErrNotFound), errors.Is(err, accounts.ErrNotFound), errors.Is(err, categories.ErrNotFound), errors.Is(err, manual.ErrNotFound), errors.Is(err, events.ErrNotFound), errors.Is(err, recurring.ErrNotFound), errors.Is(err, budgetcfg.ErrNotFound), errors.Is(err, categorize.ErrNotFound), errors.Is(err, links.ErrNotFound), errors.Is(err, receivables.ErrNotFound), errors.Is(err, counterparties.ErrNotFound), errors.Is(err, reimburse.ErrNotFound), errors.Is(err, transfers.ErrNotFound):
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, links.ErrExceeds):
		httpx.Fail(w, http.StatusUnprocessableEntity, "exceeds_remaining", "That is more than what is left to pay back. Confirm to override.")
	case errors.Is(err, ledger.ErrSplitSum):
		httpx.Fail(w, http.StatusUnprocessableEntity, "split_sum", "Split parts must add up to the transaction total.")
	case errors.Is(err, categories.ErrProtected):
		httpx.Fail(w, http.StatusConflict, "protected", "This is part of the system and cannot be removed.")
	case errors.Is(err, categories.ErrGroupNotEmpty):
		httpx.Fail(w, http.StatusConflict, "group_not_empty", "Move or delete the categories in this group first.")
	case errors.Is(err, events.ErrAlreadyUndone), errors.Is(err, events.ErrNotUndone):
		httpx.Fail(w, http.StatusConflict, "event_state", "That change is already in the state you asked for.")
	case errors.Is(err, manual.ErrNotManual):
		httpx.Fail(w, http.StatusUnprocessableEntity, "not_manual", "Only transactions you entered yourself can be edited.")
	case errors.Is(err, ledger.ErrInvalid), errors.Is(err, categories.ErrInvalid), errors.Is(err, accounts.ErrInvalid), errors.Is(err, manual.ErrInvalid), errors.Is(err, recurring.ErrInvalid), errors.Is(err, budgetcfg.ErrInvalid), errors.Is(err, categorize.ErrInvalid), errors.Is(err, links.ErrInvalid), errors.Is(err, receivables.ErrInvalid), errors.Is(err, counterparties.ErrInvalid), errors.Is(err, reimburse.ErrInvalid), errors.Is(err, transfers.ErrInvalid), errors.Is(err, errBadRequest):
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", cleanMessage(err))
	case errors.As(err, &pg) && pg.Code == "23503":
		httpx.Fail(w, http.StatusBadRequest, "unknown_reference", "That category or account does not exist.")
	default:
		s.Log.Error().Err(err).Msg("request failed")
		httpx.Fail(w, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

var errBadRequest = errors.New("bad request")

func badRequest(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errBadRequest, fmt.Sprintf(format, a...))
}

// cleanMessage drops the Go package prefix from a wrapped error.
func cleanMessage(err error) string {
	m := err.Error()
	if i := strings.Index(m, ": "); i >= 0 && !strings.Contains(m[:i], " ") {
		m = m[i+2:]
	}
	if len(m) > 0 {
		m = strings.ToUpper(m[:1]) + m[1:]
	}
	return m
}

// ---- accounts ----

type accountDTO struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Institution string `json:"institution"`
	Mask        string `json:"mask"`
	Currency    string `json:"currency"`
	Kind        string `json:"kind"`
	Active      bool   `json:"active"`
}

func toAccountDTO(a accounts.Account) accountDTO {
	return accountDTO{a.ID, a.Provider, a.Name, a.DisplayName(), a.Institution, a.Mask, a.Currency, a.Kind, a.Active}
}

func (s *server) listAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Accounts.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]accountDTO, 0, len(list))
	for _, a := range list {
		out = append(out, toAccountDTO(a))
	}
	httpx.Respond(w, map[string]any{"items": out})
}

func (s *server) createAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Currency string `json:"currency"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid account details.")
		return
	}
	var id string
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		id, err = s.Manual.CreateAccount(ctx, tx, events.ActorUser, in.Name, in.Kind, in.Currency)
		return
	}) {
		return
	}
	a, err := s.Accounts.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, toAccountDTO(a))
}

func (s *server) patchAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        *string `json:"kind"`
		DisplayName *string `json:"display_name"`
		Active      *bool   `json:"active"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid account details.")
		return
	}
	id := r.PathValue("id")
	var a accounts.Account
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		a, err = s.Accounts.Update(ctx, tx, events.ActorUser, id, accounts.Update{Kind: in.Kind, DisplayNameOverride: in.DisplayName, Active: in.Active})
		return
	}) {
		return
	}
	httpx.Respond(w, toAccountDTO(a))
}

// ---- transactions ----

type partDTO struct {
	Amount     int64    `json:"amount"`
	CategoryID string   `json:"category_id"`
	Bucket     string   `json:"bucket"`
	Tags       []string `json:"tags"`
	Note       string   `json:"note"`
}

type txnDTO struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id"`
	Date              string    `json:"date"`
	TransactedDate    *string   `json:"transacted_date"`
	Amount            int64     `json:"amount"`
	Description       string    `json:"description"`
	Payee             string    `json:"payee"`
	Memo              string    `json:"memo"`
	Pending           bool      `json:"pending"`
	MissingFromBank   bool      `json:"missing_from_bank"`
	Manual            bool      `json:"manual"`
	CategoryID        *string   `json:"category_id"`
	Bucket            *string   `json:"bucket"`
	Month             string    `json:"month"`
	MonthOverride     *string   `json:"month_override"`
	Note              string    `json:"note"`
	Tags              []string  `json:"tags"`
	Hidden            bool      `json:"hidden"`
	FromDiscretionary bool      `json:"from_discretionary"`
	Type              *string   `json:"type"`
	Reviewed          bool      `json:"reviewed"`
	Splits            []partDTO `json:"splits"`
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toTxnDTO(t ledger.Txn) txnDTO {
	d := txnDTO{
		ID: t.ID, AccountID: t.AccountID, Date: t.PostedOn.String(), Amount: int64(t.Amount), Description: t.Description, Payee: t.Payee, Memo: t.Memo,
		Pending: t.Pending, MissingFromBank: t.MissingFromBank, Manual: strings.HasPrefix(t.ProviderTxnID, manual.IDPrefix),
		CategoryID: ptr(t.CategoryID), Bucket: ptr(t.Bucket), Month: t.Month.String(), Note: t.Note, Tags: t.Tags, Hidden: t.Hidden,
		FromDiscretionary: t.FromDiscretionary, Type: ptr(t.Type), Reviewed: t.ReviewedAt != nil, Splits: []partDTO{},
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if t.TransactedOn != nil {
		s := t.TransactedOn.String()
		d.TransactedDate = &s
	}
	if t.MonthOverride != nil {
		s := t.MonthOverride.String()
		d.MonthOverride = &s
	}
	for _, p := range t.Splits {
		tags := p.Tags
		if tags == nil {
			tags = []string{}
		}
		d.Splits = append(d.Splits, partDTO{int64(p.Amount), p.CategoryID, p.Bucket, tags, p.Note})
	}
	return d
}

func (s *server) listTransactions(w http.ResponseWriter, r *http.Request) {
	pos, limit, err := httpx.PageParams(r, 50, 200)
	_ = pos
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid paging.")
		return
	}
	f := ledger.Filter{Limit: limit, Cursor: r.URL.Query().Get("cursor")}
	if a := r.URL.Query().Get("account"); a != "" {
		if !uuidRe.MatchString(a) {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid account.")
			return
		}
		f.AccountID = a
	}
	if m := r.URL.Query().Get("month"); m != "" {
		ym, err := civil.ParseYearMonth(m)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Month must look like 2026-10.")
			return
		}
		f.Month = &ym
		f.Limit = 0 // a month is bounded
	}
	f.IncludeHidden = r.URL.Query().Get("include_hidden") == "true"
	page, err := s.Ledger.View(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	items := make([]txnDTO, 0, len(page.Items))
	for _, t := range page.Items {
		items = append(items, toTxnDTO(t))
	}
	out := map[string]any{"items": items}
	if page.Next != "" {
		out["next"] = page.Next
	}
	httpx.Respond(w, out)
}

func (s *server) createTransaction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID   string `json:"account_id"`
		Date        string `json:"date"`
		Amount      int64  `json:"amount"`
		Description string `json:"description"`
		Payee       string `json:"payee"`
	}
	if httpx.Decode(r, &in) != nil || !uuidRe.MatchString(in.AccountID) {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid transaction details.")
		return
	}
	date, err := civil.ParseDate(in.Date)
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Date must look like 2026-10-05.")
		return
	}
	var id string
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		if id, err = s.Manual.AddTxn(ctx, tx, events.ActorUser, in.AccountID, date, money.Cents(in.Amount), in.Description, in.Payee); err != nil {
			return err
		}
		return s.Matching.Enqueue(ctx, tx)
	}) {
		return
	}
	t, err := s.Ledger.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, toTxnDTO(t))
}

func (s *server) getTransaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	t, err := s.Ledger.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.Respond(w, toTxnDTO(t))
}

// patchTransaction applies overlay changes (and, for manual rows, field edits).
// Absent keys are left alone; null clears where clearing makes sense.
func (s *server) patchTransaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	var body map[string]json.RawMessage
	if httpx.Decode(r, &body) != nil || len(body) == 0 {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send the fields you want to change.")
		return
	}
	var ids []int64
	ok := s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		apply := func(kind string, payload any) error {
			e, err := s.Ledger.Apply(ctx, tx, events.ActorUser, id, kind, payload, "")
			if err == nil {
				ids = append(ids, e.ID)
			}
			return err
		}
		var edit manual.Edit
		var edited bool
		for key, raw := range body {
			switch key {
			case "category_id":
				var v *string
				if err := json.Unmarshal(raw, &v); err != nil {
					return badRequest("category_id must be a string or null")
				}
				if err := apply(ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: v}); err != nil {
					return err
				}
			case "note":
				var v string
				if err := json.Unmarshal(raw, &v); err != nil || len(v) > 2000 {
					return badRequest("note must be a string of at most 2000 characters")
				}
				if err := apply(ledger.KindSetNote, ledger.SetNote{TxnID: id, Note: v}); err != nil {
					return err
				}
			case "tags":
				var v []string
				if err := json.Unmarshal(raw, &v); err != nil || len(v) > 50 {
					return badRequest("tags must be a list of at most 50 strings")
				}
				if err := apply(ledger.KindSetTags, ledger.SetTags{TxnID: id, Tags: v}); err != nil {
					return err
				}
			case "month":
				var v *string
				if err := json.Unmarshal(raw, &v); err != nil {
					return badRequest("month must be a string like 2026-10, or null")
				}
				if err := apply(ledger.KindSetMonth, ledger.SetMonth{TxnID: id, Month: v}); err != nil {
					return err
				}
			case "from_discretionary":
				var v bool
				if err := json.Unmarshal(raw, &v); err != nil {
					return badRequest("from_discretionary must be true or false")
				}
				if err := apply(ledger.KindSetFromDiscretionary, ledger.SetFromDiscretionary{TxnID: id, Value: v}); err != nil {
					return err
				}
			case "reviewed":
				var v bool
				if err := json.Unmarshal(raw, &v); err != nil {
					return badRequest("reviewed must be true or false")
				}
				if err := apply(ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: v}); err != nil {
					return err
				}
			case "type":
				var v *string
				if err := json.Unmarshal(raw, &v); err != nil {
					return badRequest("type must be a string or null")
				}
				t := ""
				if v != nil {
					t = *v
				}
				if err := apply(ledger.KindSetType, ledger.SetType{TxnID: id, Type: t}); err != nil {
					return err
				}
			case "date", "amount", "description", "payee":
				edited = true
				var err error
				switch key {
				case "date":
					var v string
					if err = json.Unmarshal(raw, &v); err == nil {
						edit.Date = &v
					}
				case "amount":
					var v int64
					if err = json.Unmarshal(raw, &v); err == nil {
						edit.Amount = &v
					}
				case "description":
					var v string
					if err = json.Unmarshal(raw, &v); err == nil {
						edit.Description = &v
					}
				case "payee":
					var v string
					if err = json.Unmarshal(raw, &v); err == nil {
						edit.Payee = &v
					}
				}
				if err != nil {
					return badRequest("%s has the wrong type", key)
				}
			default:
				return badRequest("unknown field %q", key)
			}
		}
		if edited {
			e, err := s.Manual.EditTxn(ctx, tx, events.ActorUser, id, edit)
			if err != nil {
				return err
			}
			ids = append(ids, e.ID)
		}
		return nil
	})
	if !ok {
		return
	}
	t, err := s.Ledger.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.Respond(w, map[string]any{"transaction": toTxnDTO(t), "event_ids": ids})
}

func (s *server) splitTransaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Parts []struct {
			Amount     int64    `json:"amount"`
			CategoryID string   `json:"category_id"`
			Tags       []string `json:"tags"`
			Note       string   `json:"note"`
		} `json:"parts"`
	}
	if !uuidRe.MatchString(id) || httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid split.")
		return
	}
	parts := make([]ledger.SplitPart, len(in.Parts))
	for i, p := range in.Parts {
		parts[i] = ledger.SplitPart{Amount: p.Amount, CategoryID: p.CategoryID, Tags: p.Tags, Note: p.Note}
	}
	var e events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		e, err = s.Ledger.Apply(ctx, tx, events.ActorUser, id, ledger.KindSplit, ledger.Split{TxnID: id, Parts: parts}, "")
		return
	}) {
		return
	}
	t, err := s.Ledger.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.Respond(w, map[string]any{"transaction": toTxnDTO(t), "event_ids": []int64{e.ID}})
}

func (s *server) hideTransaction(unhide bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var in struct {
			Reason string `json:"reason"`
		}
		if !uuidRe.MatchString(id) || httpx.Decode(r, &in) != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
			return
		}
		var e events.Event
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			if unhide {
				e, err = s.Ledger.Apply(ctx, tx, events.ActorUser, id, ledger.KindUnhide, ledger.Unhide{TxnID: id}, "")
			} else {
				e, err = s.Ledger.Apply(ctx, tx, events.ActorUser, id, ledger.KindHide, ledger.Hide{TxnID: id, Reason: in.Reason}, "")
			}
			return
		}) {
			return
		}
		t, err := s.Ledger.Get(r.Context(), id)
		if err != nil {
			s.fail(w, err)
			return
		}
		httpx.Respond(w, map[string]any{"transaction": toTxnDTO(t), "event_ids": []int64{e.ID}})
	}
}

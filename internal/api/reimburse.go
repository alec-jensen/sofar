package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/transfers"
)

type receivableDTO struct {
	ID             string  `json:"id"`
	CounterpartyID *string `json:"counterparty_id"`
	ExpenseID      string  `json:"expense_id"`
	PartNo         *int    `json:"part_no"`
	Amount         int64   `json:"amount"`
	AmountMax      *int64  `json:"amount_max"`
	ExpectedBy     *string `json:"expected_by"`
	NettedNow      bool    `json:"netted_now"`
	Note           string  `json:"note"`
	Source         string  `json:"source"`
	OpenedOn       string  `json:"opened_on"`
	Received       int64   `json:"received"`
	Status         string  `json:"status"`
	Overdue        bool    `json:"overdue"`
	AgeDays        int     `json:"age_days"`
	Unrecovered    int64   `json:"unrecovered"`
}

func toReceivableDTO(r receivables.Receivable) receivableDTO {
	d := receivableDTO{ID: r.ID, CounterpartyID: r.CounterpartyID, ExpenseID: r.ExpenseID, PartNo: r.PartNo, Amount: r.Amount, AmountMax: r.AmountMax, NettedNow: r.NettedNow,
		Note: r.Note, Source: r.Source, OpenedOn: r.OpenedOn.String(), Received: r.ReceivedAmount, Status: string(r.Status), Overdue: r.Overdue, AgeDays: r.AgeDays, Unrecovered: r.Unrecovered()}
	if r.ExpectedBy != nil {
		s := r.ExpectedBy.String()
		d.ExpectedBy = &s
	}
	return d
}

type heldDTO struct {
	TxnID        string `json:"txn_id"`
	State        string `json:"state"`
	Since        string `json:"since"`
	GraceUntil   string `json:"grace_until"`
	HoldUntil    string `json:"hold_until"`
	Declared     bool   `json:"declared"`
	HintMerchant string `json:"hint_merchant"`
	HintAmount   *int64 `json:"hint_amount"`
	Amount       int64  `json:"amount"`
	Description  string `json:"description"`
	Suggestions  int    `json:"suggestions"`
}

type linkDTO struct {
	ID           int64   `json:"id"`
	PaymentID    string  `json:"payment_id"`
	ExpenseID    string  `json:"expense_id"`
	PartNo       *int    `json:"part_no"`
	ReceivableID *string `json:"receivable_id"`
	Amount       int64   `json:"amount"`
	State        string  `json:"state"`
	CreatedBy    string  `json:"created_by"`
}

func toLinkDTO(l links.Link) linkDTO {
	return linkDTO{ID: l.ID, PaymentID: l.PaymentID, ExpenseID: l.ExpenseID, PartNo: l.PartNo, ReceivableID: l.ReceivableID, Amount: l.Amount, State: l.State, CreatedBy: l.CreatedBy}
}

// owed is the "owed to you" card: receivables with their aging, and payments waiting for their expense.
func (s *server) owed(w http.ResponseWriter, r *http.Request) {
	rs, err := s.Receivables.List(r.Context(), s.DB, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	hs, err := s.Reimburse.ListHeld(r.Context(), s.DB)
	if err != nil {
		s.fail(w, err)
		return
	}
	al, err := s.Receivables.Aging(r.Context(), s.DB, s.Reimburse.AgingThresholds(r.Context(), s.DB))
	if err != nil {
		s.fail(w, err)
		return
	}
	recv := []receivableDTO{}
	for _, x := range rs {
		recv = append(recv, toReceivableDTO(x))
	}
	held := []heldDTO{}
	for _, h := range hs {
		held = append(held, heldDTO{h.TxnID, h.State, h.Since.String(), h.GraceUntil.String(), h.HoldUntil.String(), h.Declared, h.HintMerchant, h.HintAmount, h.Amount, h.Description, h.Suggestions})
	}
	alerts := []map[string]any{}
	for _, a := range al {
		alerts = append(alerts, map[string]any{"receivable_id": a.ReceivableID, "age_days": a.AgeDays, "threshold": a.Threshold})
	}
	httpx.Respond(w, map[string]any{"receivables": recv, "held": held, "alerts": alerts})
}

func (s *server) createReceivable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpenseID      string  `json:"expense_id"`
		PartNo         *int    `json:"part_no"`
		Amount         int64   `json:"amount"`
		AmountMax      *int64  `json:"amount_max"`
		CounterpartyID *string `json:"counterparty_id"`
		ExpectedBy     *string `json:"expected_by"`
		NettedNow      *bool   `json:"netted_now"`
		Note           string  `json:"note"`
		Source         string  `json:"source"`
	}
	if httpx.Decode(r, &in) != nil || in.ExpenseID == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Say which expense and how much is owed.")
		return
	}
	p := receivables.Params{ExpenseID: in.ExpenseID, PartNo: in.PartNo, Amount: in.Amount, AmountMax: in.AmountMax, CounterpartyID: in.CounterpartyID, NettedNow: in.NettedNow, Note: in.Note, Source: in.Source}
	if in.ExpectedBy != nil {
		d, err := civil.ParseDate(*in.ExpectedBy)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "expected_by must look like 2026-10-05.")
			return
		}
		p.ExpectedBy = &d
	}
	var rec receivables.Receivable
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		rec, err = s.Receivables.Create(ctx, tx, events.ActorUser, p)
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, toReceivableDTO(rec))
}

// receivableReceived marks a receivable received: by a real payment, or by cash.
func (s *server) receivableReceived(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PaymentID string `json:"payment_id"`
		Amount    int64  `json:"amount"`
		Cash      *struct {
			AccountID string `json:"account_id"`
			Date      string `json:"date"`
		} `json:"cash"`
	}
	if httpx.Decode(r, &in) != nil || (in.PaymentID == "") == (in.Cash == nil) {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Give a payment, or say it was cash.")
		return
	}
	id := r.PathValue("id")
	var rec receivables.Receivable
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		if in.Cash != nil {
			d, err := civil.ParseDate(in.Cash.Date)
			if err != nil {
				return badRequest("date must look like 2026-10-05")
			}
			if err = s.Reimburse.ReceiveCashLearn(ctx, tx, events.ActorUser, id, in.Cash.AccountID, d, in.Amount); err != nil {
				return err
			}
		} else if err := s.Reimburse.ReceiveLearn(ctx, tx, events.ActorUser, id, in.PaymentID, in.Amount); err != nil {
			return err
		}
		var err error
		rec, err = s.Receivables.Get(ctx, tx, id)
		return err
	}) {
		return
	}
	httpx.Respond(w, toReceivableDTO(rec))
}

func (s *server) receivableWriteOff(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var rec receivables.Receivable
	var ev events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		if rec, err = s.Receivables.WriteOff(ctx, tx, events.ActorUser, r.PathValue("id")); err != nil {
			return err
		}
		tx.QueryRowContext(ctx, `SELECT id FROM events WHERE kind=$1 AND subject_id=$2 ORDER BY id DESC LIMIT 1`, receivables.KindWriteOff, rec.ID).Scan(&ev.ID)
		return nil
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"receivable": toReceivableDTO(rec), "event_id": ev.ID})
}

type optionDTO struct {
	ExpenseID    string   `json:"expense_id"`
	ReceivableID *string  `json:"receivable_id"`
	Amount       int64    `json:"amount"`
	Confidence   float64  `json:"confidence"`
	Why          []string `json:"why"`
	Weak         bool     `json:"weak"`
	Over         bool     `json:"over"`
	Group        int      `json:"group"`
}

func toDecisionDTO(d reimburse.Decision) map[string]any {
	opts := []optionDTO{}
	for _, o := range d.Options {
		why := o.Why
		if why == nil {
			why = []string{}
		}
		opts = append(opts, optionDTO{o.ExpenseID, ptr(o.ReceivableID), o.Amount, o.P, why, o.Features.Weak, o.Features.Over, o.Group})
	}
	out := map[string]any{"payment_id": d.PaymentID, "decision": string(d.Kind), "source": d.Source, "reason": d.Reason, "options": opts, "pattern_id": ptr(d.PatternID)}
	if d.Payer != nil {
		out["payer"] = map[string]any{"id": d.Payer.ID, "name": d.Payer.Name, "policy": d.Payer.Policy}
	} else {
		out["payer"] = nil
	}
	return out
}

// paymentCandidates is the link picker: every candidate the matcher considered
// for one payment, with scores and reasons, plus the suggestions it stored.
func (s *server) paymentCandidates(w http.ResponseWriter, r *http.Request) {
	d, err := s.Reimburse.Debug(r.Context(), s.DB, r.PathValue("txn"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.Respond(w, toDecisionDTO(d))
}

// debugMatch is the matching debugger: the same replay, for any payment.
func (s *server) debugMatch(w http.ResponseWriter, r *http.Request) {
	d, err := s.Reimburse.Debug(r.Context(), s.DB, r.PathValue("txn"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.Respond(w, toDecisionDTO(d))
}

func (s *server) createLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PaymentID string `json:"payment_id"`
		ExpenseID string `json:"expense_id"`
		PartNo    *int   `json:"part_no"`
		Amount    int64  `json:"amount"`
		Override  bool   `json:"override"`
		// Suggestion accepts the stored suggestion of that rank for the payment instead.
		Suggestion int `json:"suggestion"`
	}
	if httpx.Decode(r, &in) != nil || in.PaymentID == "" || (in.ExpenseID == "" && in.Suggestion == 0) {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Say which payment and which expense.")
		return
	}
	var l links.Link
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		if in.Suggestion > 0 {
			l, err = s.Reimburse.AcceptSuggestion(ctx, tx, events.ActorUser, in.PaymentID, in.Suggestion)
			return
		}
		l, err = s.Reimburse.ManualLink(ctx, tx, events.ActorUser, links.Params{PaymentID: in.PaymentID, ExpenseID: in.ExpenseID, PartNo: in.PartNo, Amount: in.Amount, Override: in.Override})
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, toLinkDTO(l))
}

func linkID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	return id, true
}

func (s *server) linkAction(act func(ctx context.Context, tx *sql.Tx, id int64) (links.Link, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := linkID(w, r)
		if !ok {
			return
		}
		var l links.Link
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			l, err = act(ctx, tx, id)
			return
		}) {
			return
		}
		httpx.Respond(w, toLinkDTO(l))
	}
}

func (s *server) confirmLink(w http.ResponseWriter, r *http.Request) {
	s.linkAction(func(ctx context.Context, tx *sql.Tx, id int64) (links.Link, error) {
		return s.Reimburse.Confirm(ctx, tx, events.ActorUser, id)
	})(w, r)
}

func (s *server) rejectLink(w http.ResponseWriter, r *http.Request) {
	s.linkAction(func(ctx context.Context, tx *sql.Tx, id int64) (links.Link, error) {
		return s.Reimburse.Reject(ctx, tx, events.ActorUser, id)
	})(w, r)
}

func (s *server) deleteLink(w http.ResponseWriter, r *http.Request) {
	s.linkAction(func(ctx context.Context, tx *sql.Tx, id int64) (links.Link, error) {
		return s.Reimburse.Unlink(ctx, tx, events.ActorUser, id)
	})(w, r)
}

type counterpartyDTO struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Policy  string   `json:"policy"`
	AutoNet bool     `json:"auto_net"`
	Aliases []string `json:"aliases"`
	Profile *struct {
		Paybacks    int     `json:"paybacks"`
		Others      int     `json:"others"`
		MedianLag   int     `json:"median_lag_days"`
		MedianShare float64 `json:"median_share"`
		Reliability float64 `json:"reliability"`
		Mixed       bool    `json:"mixed"`
	} `json:"profile"`
}

func (s *server) counterpartyDTO(ctx context.Context, c counterparties.Counterparty) counterpartyDTO {
	d := counterpartyDTO{ID: c.ID, Name: c.Name, Policy: c.Policy, AutoNet: c.AutoNet, Aliases: c.Aliases}
	if d.Aliases == nil {
		d.Aliases = []string{}
	}
	if pr, err := s.Reimburse.Profile(ctx, s.DB, c.ID); err == nil {
		d.Profile = &struct {
			Paybacks    int     `json:"paybacks"`
			Others      int     `json:"others"`
			MedianLag   int     `json:"median_lag_days"`
			MedianShare float64 `json:"median_share"`
			Reliability float64 `json:"reliability"`
			Mixed       bool    `json:"mixed"`
		}{pr.Paybacks, pr.Others, pr.MedianLag, pr.MedianShare, pr.Reliability, pr.Mixed}
	}
	return d
}

func (s *server) listCounterparties(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Counterparties.List(r.Context(), s.DB)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []counterpartyDTO{}
	for _, c := range cs {
		out = append(out, s.counterpartyDTO(r.Context(), c))
	}
	httpx.Respond(w, map[string]any{"items": out})
}

func (s *server) createCounterparty(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
	}
	if httpx.Decode(r, &in) != nil || in.Name == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "A counterparty needs a name.")
		return
	}
	var c counterparties.Counterparty
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		c, err = s.Counterparties.Create(ctx, tx, events.ActorUser, in.Name, in.Aliases...)
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, s.counterpartyDTO(r.Context(), c))
}

func (s *server) patchCounterparty(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      *string `json:"name"`
		Policy    *string `json:"policy"`
		AutoNet   *bool   `json:"auto_net"`
		AddAlias  *string `json:"add_alias"`
		MergeInto *string `json:"merge_into"`
	}
	if httpx.Decode(r, &in) != nil || (in.Name == nil && in.Policy == nil && in.AutoNet == nil && in.AddAlias == nil && in.MergeInto == nil) {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Nothing to change.")
		return
	}
	id := r.PathValue("id")
	var c counterparties.Counterparty
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		if in.Name != nil {
			if err = s.Counterparties.Rename(ctx, tx, events.ActorUser, id, *in.Name); err != nil {
				return
			}
		}
		if in.Policy != nil {
			if err = s.Counterparties.SetPolicy(ctx, tx, events.ActorUser, id, *in.Policy); err != nil {
				return
			}
		}
		if in.AutoNet != nil {
			if err = s.Counterparties.SetAutoNet(ctx, tx, events.ActorUser, id, *in.AutoNet); err != nil {
				return
			}
		}
		if in.AddAlias != nil {
			if _, err = s.Counterparties.AddAlias(ctx, tx, events.ActorUser, id, *in.AddAlias); err != nil {
				return
			}
		}
		target := id
		if in.MergeInto != nil {
			if err = s.Counterparties.Merge(ctx, tx, events.ActorUser, id, *in.MergeInto); err != nil {
				return
			}
			target = *in.MergeInto
		}
		c, err = s.Counterparties.Get(ctx, tx, target)
		return
	}) {
		return
	}
	httpx.Respond(w, s.counterpartyDTO(r.Context(), c))
}

type askDTO struct {
	Transaction txnDTO   `json:"transaction"`
	Default     string   `json:"default"`
	Options     []string `json:"options"`
}

// transferAsks lists lone transfer-looking transactions that wait for an answer.
func (s *server) transferAsks(w http.ResponseWriter, r *http.Request) {
	as, err := s.Transfers.Asks(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []askDTO{}
	for _, a := range as {
		out = append(out, askDTO{toTxnDTO(a.Txn), a.Default, a.Options})
	}
	httpx.Respond(w, map[string]any{"items": out})
}

func (s *server) answerTransferAsk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Choice string `json:"choice"`
	}
	if httpx.Decode(r, &in) != nil || in.Choice == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Choose neutral, savings or spending.")
		return
	}
	var ev events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		ev, err = s.Transfers.Resolve(ctx, tx, events.ActorUser, r.PathValue("txn"), in.Choice)
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": ev.ID})
}

func (s *server) unpairTransfer(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		return s.Transfers.Unpair(ctx, tx, events.ActorUser, r.PathValue("txn"))
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"ok": true})
}

var _ = transfers.CatTransfer

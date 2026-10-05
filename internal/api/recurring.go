package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/recurrence"
	"github.com/alec-jensen/sofar/internal/recurring"
)

type scheduleIn struct {
	Kind         string `json:"kind"` // monthly, semi_monthly, nth_weekday, last_business_day, interval_days
	Day          int    `json:"day"`
	Days         []int  `json:"days"`
	Nth          int    `json:"nth"`
	Weekday      int    `json:"weekday"`
	EveryMonths  int    `json:"every_months"`
	IntervalDays int    `json:"interval_days"`
	Anchor       string `json:"anchor"`
}

func (in scheduleIn) schedule() (recurrence.Schedule, error) {
	var s recurrence.Schedule
	if in.Anchor != "" {
		a, err := civil.ParseDate(in.Anchor)
		if err != nil {
			return s, badRequest("anchor must look like 2026-10-05")
		}
		s.Anchor = a
	}
	switch in.Kind {
	case "interval_days":
		s.Kind, s.IntervalDays = recurrence.KindInterval, in.IntervalDays
	case "monthly":
		s.Kind, s.Calendar = recurrence.KindCalendar, recurrence.CalendarRule{Kind: recurrence.Monthly, Day: in.Day, EveryMonths: in.EveryMonths}
	case "semi_monthly":
		s.Kind, s.Calendar = recurrence.KindCalendar, recurrence.CalendarRule{Kind: recurrence.SemiMonthly}
		if len(in.Days) == 2 {
			s.Calendar.Days = [2]int{in.Days[0], in.Days[1]}
		}
	case "nth_weekday":
		s.Kind, s.Calendar = recurrence.KindCalendar, recurrence.CalendarRule{Kind: recurrence.NthWeekday, Nth: in.Nth, Weekday: time.Weekday(in.Weekday)}
	case "last_business_day":
		s.Kind, s.Calendar = recurrence.KindCalendar, recurrence.CalendarRule{Kind: recurrence.LastBusinessDay}
	default:
		return s, badRequest("unknown schedule kind %q", in.Kind)
	}
	if err := s.Validate(); err != nil {
		return s, badRequest("that schedule is not usable")
	}
	return s, nil
}

type itemDTO struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	CategoryID   *string `json:"category_id"`
	Amount       int64   `json:"amount"`
	Variable     bool    `json:"variable"`
	ActiveFrom   string  `json:"active_from"`
	EndedOn      *string `json:"ended_on"`
	NextDate     *string `json:"next_date"`
	Installments int     `json:"installments_total"`
}

func (s *server) toItemDTO(it recurring.Item) itemDTO {
	d := itemDTO{ID: it.ID, Kind: string(it.Kind), Name: it.Name, CategoryID: ptr(it.CategoryID), Amount: int64(it.Amount), Variable: it.Variable,
		ActiveFrom: it.ActiveFrom.String(), Installments: it.InstallmentsTotal}
	if it.EndedOn != nil {
		e := it.EndedOn.String()
		d.EndedOn = &e
	}
	today := civil.DateOf(s.Recompute.Now().In(s.Recompute.Home))
	if sch, ok := it.ScheduleAt(today); ok && (it.EndedOn == nil || !it.EndedOn.Before(today)) {
		if occ := sch.Occurrences(today, today.AddDays(800), s.Recompute.Home); len(occ) > 0 {
			n := occ[0].Date.String()
			d.NextDate = &n
		}
	}
	return d
}

type candidateDTO struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	CategoryID  *string `json:"category_id"`
	Amount      int64   `json:"amount"`
	Variable    bool    `json:"variable"`
	Occurrences int     `json:"occurrences"`
	LastDate    string  `json:"last_date"`
	NextDate    string  `json:"next_date"`
	AfterOne    bool    `json:"after_one"`
}

type signalDTO struct {
	ItemID string  `json:"item_id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	On     *string `json:"on"`
	Amount int64   `json:"amount"`
}

func (s *server) listRecurring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := s.Recurring.List(ctx, s.DB)
	if err != nil {
		s.fail(w, err)
		return
	}
	cands, err := s.Recurring.Candidates(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	sigs, err := s.Recompute.Signals(ctx, s.DB, civil.DateOf(s.Recompute.Now().In(s.Recompute.Home)))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"items": []itemDTO{}, "candidates": []candidateDTO{}, "signals": []signalDTO{}}
	its := []itemDTO{}
	for _, it := range items {
		its = append(its, s.toItemDTO(it))
	}
	cs := []candidateDTO{}
	for _, c := range cands {
		cs = append(cs, candidateDTO{c.Key, c.Name, string(c.Kind), ptr(c.CategoryID), int64(c.Amount), c.Variable, c.Occurrences, c.LastDate.String(), c.NextDate.String(), c.AfterOne})
	}
	ss := []signalDTO{}
	for _, sg := range sigs {
		d := signalDTO{ItemID: sg.ItemID, Name: sg.Name, Kind: sg.Kind, Amount: int64(sg.Amount)}
		if !sg.On.IsZero() {
			on := sg.On.String()
			d.On = &on
		}
		ss = append(ss, d)
	}
	out["items"], out["candidates"], out["signals"] = its, cs, ss
	httpx.Respond(w, out)
}

func (s *server) confirmCandidate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
	}
	if httpx.Decode(r, &in) != nil || in.Key == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Say which candidate to confirm.")
		return
	}
	cands, err := s.Recurring.Candidates(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, c := range cands {
		if c.Key != in.Key {
			continue
		}
		var it recurring.Item
		if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
			it, err = s.Recurring.Confirm(ctx, tx, events.ActorUser, c, recurrence.ItemKind(in.Kind))
			return
		}) {
			return
		}
		httpx.RespondStatus(w, http.StatusCreated, s.toItemDTO(it))
		return
	}
	httpx.Fail(w, http.StatusNotFound, "not_found", "That candidate is no longer there.")
}

func (s *server) dismissCandidate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key       string `json:"key"`
		Direction string `json:"direction"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var e events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		e, err = s.Recurring.Dismiss(ctx, tx, events.ActorUser, in.Key, in.Direction)
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": e.ID})
}

type createItemIn struct {
	Kind              string     `json:"kind"`
	Name              string     `json:"name"`
	Amount            int64      `json:"amount"`
	Variable          bool       `json:"variable"`
	CategoryID        string     `json:"category_id"`
	Schedule          scheduleIn `json:"schedule"`
	InstallmentsTotal int        `json:"installments_total"`
}

func (s *server) createRecurring(w http.ResponseWriter, r *http.Request) {
	var in createItemIn
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid recurring item.")
		return
	}
	var it recurring.Item
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		sch, err := in.Schedule.schedule()
		if err != nil {
			return err
		}
		it, err = s.Recurring.Create(ctx, tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemKind(in.Kind), Name: in.Name, Amount: money.Cents(in.Amount),
			Variable: in.Variable, CategoryID: in.CategoryID, Schedule: sch, InstallmentsTotal: in.InstallmentsTotal})
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, s.toItemDTO(it))
}

func (s *server) markRecurring(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in createItemIn
	if !uuidRe.MatchString(id) || httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var it recurring.Item
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		sch, err := in.Schedule.schedule()
		if err != nil {
			return err
		}
		it, err = s.Recurring.MarkAsRecurring(ctx, tx, events.ActorUser, id, recurring.MarkParams{Kind: recurrence.ItemKind(in.Kind), Schedule: sch,
			Amount: money.Cents(in.Amount), Variable: in.Variable, CategoryID: in.CategoryID, InstallmentsTotal: in.InstallmentsTotal})
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, s.toItemDTO(it))
}

func (s *server) endRecurring(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On string `json:"on"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	on := civil.DateOf(s.Recompute.Now().In(s.Recompute.Home))
	if in.On != "" {
		d, err := civil.ParseDate(in.On)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Date must look like 2026-10-05.")
			return
		}
		on = d
	}
	var e events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		e, err = s.Recurring.EndItem(ctx, tx, events.ActorUser, r.PathValue("id"), on)
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": e.ID})
}

// forecast is next month's plan so far and the pot decomposition (suggestions only).
func (s *server) forecast(w http.ResponseWriter, r *http.Request) {
	ym, err := civil.ParseYearMonth(r.PathValue("ym"))
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Month must look like 2026-10.")
		return
	}
	f, err := s.Recompute.Forecast(r.Context(), s.DB, ym.Prev())
	if err != nil {
		s.fail(w, err)
		return
	}
	sug, err := s.Recompute.Decompose(r.Context(), s.DB, ym)
	if err != nil {
		s.fail(w, err)
		return
	}
	type sugDTO struct {
		Stream  string `json:"stream"`
		Kind    string `json:"kind"`
		Amount  int64  `json:"amount"`
		Suggest string `json:"suggest"`
		Label   string `json:"label"`
	}
	ss := []sugDTO{}
	for _, x := range sug {
		ss = append(ss, sugDTO{x.Stream, x.Kind, int64(x.Amount), x.Suggest, x.Label})
	}
	extras := f.Extras
	if extras == nil {
		extras = []string{}
	}
	httpx.Respond(w, map[string]any{"month": ym.String(), "source_month": ym.Prev().String(), "plan_so_far": int64(f.SoFar), "plan_expected": int64(f.Expected), "extras": extras, "suggestions": ss})
}

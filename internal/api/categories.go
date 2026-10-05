package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
)

type categoryDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

type groupDTO struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Bucket     string        `json:"bucket"`
	IsDefault  bool          `json:"is_default"`
	Categories []categoryDTO `json:"categories"`
}

func (s *server) listCategories(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Categories.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]groupDTO, 0, len(groups))
	for _, g := range groups {
		d := groupDTO{ID: g.ID, Name: g.Name, Bucket: g.Bucket, IsDefault: g.IsDefault, Categories: []categoryDTO{}}
		for _, c := range g.Categories {
			d.Categories = append(d.Categories, categoryDTO{c.ID, c.Name, c.IsDefault})
		}
		out = append(out, d)
	}
	httpx.Respond(w, map[string]any{"groups": out})
}

func (s *server) createGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Bucket string `json:"bucket"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid group.")
		return
	}
	var g categories.Group
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		g, err = s.Categories.CreateGroup(ctx, tx, events.ActorUser, in.Name, in.Bucket)
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, groupDTO{ID: g.ID, Name: g.Name, Bucket: g.Bucket, Categories: []categoryDTO{}})
}

func (s *server) createCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GroupID string `json:"group_id"`
		Name    string `json:"name"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "A category needs a group and a name.")
		return
	}
	var c categories.Category
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		c, err = s.Categories.CreateCategory(ctx, tx, events.ActorUser, in.GroupID, in.Name)
		return
	}) {
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, categoryDTO{ID: c.ID, Name: c.Name})
}

func (s *server) renameCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid name.")
		return
	}
	id := r.PathValue("id")
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) error {
		return s.Categories.RenameCategory(ctx, tx, events.ActorUser, id, in.Name)
	}) {
		return
	}
	httpx.Respond(w, map[string]string{"id": id, "name": in.Name})
}

func (s *server) mergeCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Into string `json:"into"`
	}
	if httpx.Decode(r, &in) != nil || in.Into == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Say which category to merge into.")
		return
	}
	var e events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		e, err = s.Categories.Merge(ctx, tx, events.ActorUser, r.PathValue("id"), in.Into)
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": e.ID})
}

// deleteCategory needs a reassignment target; without ?reassign_to it uses
// "other" in the same bucket (S1a).
func (s *server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	var e events.Event
	if !s.withTx(w, r, func(ctx context.Context, tx *sql.Tx) (err error) {
		e, err = s.Categories.Delete(ctx, tx, events.ActorUser, r.PathValue("id"), r.URL.Query().Get("reassign_to"))
		return
	}) {
		return
	}
	httpx.Respond(w, map[string]any{"event_id": e.ID})
}

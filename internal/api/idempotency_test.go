//go:build integration

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/testkit"
)

func TestAPI_IdempotencyKeyReplaysResult(t *testing.T) {
	s := &server{Deps: Deps{DB: testkit.DB(t), Log: zerolog.Nop()}}
	calls := 0
	h := s.idempotent(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"n":` + string(rune('0'+calls)) + `}`))
	})
	send := func(key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/things", strings.NewReader(body))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	first := send("k1", `{"a":1}`)
	retry := send("k1", `{"a":1}`)
	if calls != 1 || first.Code != 201 || retry.Code != 201 || retry.Body.String() != first.Body.String() || retry.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("calls=%d first=%d/%s retry=%d/%s", calls, first.Code, first.Body, retry.Code, retry.Body)
	}
	if w := send("k1", `{"a":2}`); w.Code != 422 || calls != 1 {
		t.Errorf("same key with a different body must be refused: %d (calls %d)", w.Code, calls)
	}
	send("k2", `{"a":1}`)
	if calls != 2 {
		t.Errorf("a new key runs the handler, calls=%d", calls)
	}
	send("", `{}`)
	send("", `{}`)
	if calls != 4 {
		t.Errorf("requests without a key always run, calls=%d", calls)
	}
}

func TestAPI_ServerErrorsAreNotStored(t *testing.T) {
	s := &server{Deps: Deps{DB: testkit.DB(t), Log: zerolog.Nop()}}
	calls := 0
	h := s.idempotent(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	})
	do := func() int {
		r := httptest.NewRequest("POST", "/x", strings.NewReader("{}"))
		r.Header.Set("Idempotency-Key", "k")
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	if do() != 500 || do() != 200 {
		t.Fatal("a failed first attempt must not poison the key")
	}
}

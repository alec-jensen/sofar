package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/importer"
)

// maxImportBody is the request size for file uploads (a base64 or text file up to MaxBytes).
const maxImportBody = importer.MaxBytes*4/3 + 64<<10

type plannedDTO struct {
	Date        string `json:"date"`
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason,omitempty"`
}

type importDTO struct {
	ID       int64              `json:"id"`
	Status   string             `json:"status"`
	Filename string             `json:"filename"`
	Summary  importer.Summary   `json:"summary"`
	Rows     []plannedDTO       `json:"rows"`
	Skipped  []importer.Skipped `json:"skipped"`
}

const previewRows = 200

func toImportDTO(b importer.Batch) importDTO {
	d := importDTO{ID: b.ID, Status: b.Status, Filename: b.Filename, Summary: b.Summary, Rows: []plannedDTO{}, Skipped: b.Skipped}
	if d.Skipped == nil {
		d.Skipped = []importer.Skipped{}
	}
	for i, p := range b.Plan {
		if i >= previewRows {
			break
		}
		d.Rows = append(d.Rows, plannedDTO{p.Row.Date.String(), int64(p.Row.Amount), p.Row.Description, string(p.Outcome), p.Reason})
	}
	return d
}

// createImport parses an uploaded file and returns the dry run. Nothing is written to the ledger.
func (s *server) createImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string            `json:"account_id"`
		Format    string            `json:"format"`
		Filename  string            `json:"filename"`
		Content   string            `json:"content"`
		Base64    bool              `json:"base64"`
		Mapping   *importer.Mapping `json:"mapping"`
		DayFirst  bool              `json:"day_first"`
	}
	if httpx.Decode(r, &in) != nil || !uuidRe.MatchString(in.AccountID) || in.Content == "" {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Send an account, a format and the file contents.")
		return
	}
	data := []byte(in.Content)
	if in.Base64 {
		var err error
		if data, err = base64.StdEncoding.DecodeString(in.Content); err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "The file contents are not valid base64.")
			return
		}
	}
	var parsed importer.Parsed
	var err error
	switch strings.ToLower(in.Format) {
	case "csv":
		if in.Mapping == nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "A CSV import needs a column mapping.")
			return
		}
		parsed, err = importer.ParseCSV(strings.NewReader(string(data)), *in.Mapping)
	case "ofx", "qfx":
		parsed, err = importer.ParseOFX(strings.NewReader(string(data)))
	case "qif":
		parsed, err = importer.ParseQIF(strings.NewReader(string(data)), in.DayFirst)
	default:
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Format must be csv, ofx, qfx or qif.")
		return
	}
	switch {
	case errors.Is(err, importer.ErrTooLarge):
		httpx.Fail(w, http.StatusRequestEntityTooLarge, "too_large", "That file is too large.")
		return
	case errors.Is(err, importer.ErrFormat):
		httpx.Fail(w, http.StatusUnprocessableEntity, "bad_file", "That file could not be read as "+strings.ToLower(in.Format)+".")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	b, err := s.Importer.DryRun(r.Context(), in.AccountID, strings.ToLower(in.Format), clipName(in.Filename), parsed)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.RespondStatus(w, http.StatusCreated, toImportDTO(b))
}

func clipName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func batchID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	return id, true
}

func (s *server) importFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, importer.ErrNoBatch):
		httpx.Fail(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, importer.ErrBatchState):
		httpx.Fail(w, http.StatusConflict, "import_state", "This import was already applied or undone.")
	default:
		s.fail(w, err)
	}
}

func (s *server) getImport(w http.ResponseWriter, r *http.Request) {
	id, ok := batchID(w, r)
	if !ok {
		return
	}
	b, err := s.Importer.Get(r.Context(), id)
	if err != nil {
		s.importFail(w, err)
		return
	}
	httpx.Respond(w, toImportDTO(b))
}

func (s *server) applyImport(w http.ResponseWriter, r *http.Request) {
	id, ok := batchID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	e, sum, err := s.Importer.Apply(r.Context(), id, events.ActorUser)
	if err != nil {
		s.importFail(w, err)
		return
	}
	// new rows may pair, match or release held payments: one keyed job, in its own transaction
	if tx, err := s.DB.BeginTx(r.Context(), nil); err == nil {
		if s.Matching.Enqueue(r.Context(), tx) == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	httpx.Respond(w, map[string]any{"event_id": e.ID, "summary": sum})
}

func (s *server) undoImport(w http.ResponseWriter, r *http.Request) {
	id, ok := batchID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if httpx.Decode(r, &empty) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	var eventID int64
	err := s.DB.QueryRowContext(r.Context(), `SELECT applied_event_id FROM import_batches WHERE id=$1 AND status='applied'`, id).Scan(&eventID)
	if err != nil {
		httpx.Fail(w, http.StatusConflict, "import_state", "Only an applied import can be undone.")
		return
	}
	u, err := s.Importer.Undo(r.Context(), events.ActorUser, eventID)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.Respond(w, map[string]any{"event_id": u.ID})
}

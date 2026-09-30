package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/rs/zerolog/log"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SimpleFIN Bridge refreshes bank data about once a day and asks clients to
// stay near 24 requests a day, so fetches are spaced out rather than polled hard.
const (
	pollEvery        = 4 * time.Hour
	minFetchInterval = time.Hour
	// SimpleFIN recommends at most 45 days per request (end is a day ahead).
	recentWindow   = 44 * 24 * time.Hour
	backfillChunk  = 45 * 24 * time.Hour
	backfillChunks = 9
)

var errNotConnected = errors.New("Connect SimpleFIN first")

type sfinError struct {
	Code      string `json:"code"`
	Message   string `json:"msg"`
	ConnID    string `json:"conn_id"`
	AccountID string `json:"account_id"`
}
type sfinConnection struct {
	ID     string `json:"conn_id"`
	Name   string `json:"name"`
	OrgURL string `json:"org_url"`
	URL    string `json:"sfin_url"`
}
type sfinTransaction struct {
	ID           string      `json:"id"`
	Posted       json.Number `json:"posted"`
	Amount       string      `json:"amount"`
	Description  string      `json:"description"`
	Payee        string      `json:"payee"`
	TransactedAt json.Number `json:"transacted_at"`
	Pending      bool        `json:"pending"`
}
type sfinAccount struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	ConnID       string            `json:"conn_id"`
	Currency     string            `json:"currency"`
	Balance      string            `json:"balance"`
	Transactions []sfinTransaction `json:"transactions"`
	Org          *struct {
		Name   string `json:"name"`
		Domain string `json:"domain"`
		URL    string `json:"sfin-url"`
	} `json:"org"`
}
type sfinAccountSet struct {
	Errlist     []sfinError      `json:"errlist"`
	Errors      []string         `json:"errors"`
	Connections []sfinConnection `json:"connections"`
	Accounts    []sfinAccount    `json:"accounts"`
}

// cents converts a SimpleFIN numeric string to integer cents without float drift.
func cents(amount string) (int64, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok {
		return 0, fmt.Errorf("invalid amount %q", amount)
	}
	r.Mul(r, big.NewRat(100, 1))
	f, _ := r.Float64()
	if math.IsInf(f, 0) || math.Abs(f) > 1e13 {
		return 0, fmt.Errorf("amount out of range %q", amount)
	}
	return int64(math.Round(f)), nil
}

// unix reads a SimpleFIN timestamp, which servers send as a number or numeric string.
func unix(n json.Number) int64 {
	v, e := n.Int64()
	if e != nil {
		f, fe := n.Float64()
		if fe != nil {
			return 0
		}
		return int64(f)
	}
	return v
}

func accountKey(conn, id string) string {
	h := sha256.Sum256([]byte(conn + "\x00" + id))
	return "sf-" + hex.EncodeToString(h[:10])
}

// inferType guesses how an account should be treated from its name and
// institution; the user can change it.
func inferType(name, institution string) (string, string) {
	n := " " + strings.ToLower(name) + " "
	inst := strings.ToLower(institution)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("brokerage", "invest", " ira", "401k", "401(k)", "403b", "roth", "retire", " hsa", "529", "stock", "crypto", "trading"):
		return "investment", "brokerage"
	case has("credit", " card", "visa", "mastercard", "amex", "american express", "discover"):
		return "credit", "credit card"
	case has("sav", "money market", " mm ", " cd ", "certificate", "share"):
		return "depository", "savings"
	case !has("checking", "cash management", "debit", "spend") && func() bool {
		for _, b := range []string{"fidelity", "vanguard", "schwab", "robinhood", "e*trade", "etrade", "webull", "merrill", "interactive brokers", "wealthfront", "betterment", "acorns", "stash", "m1 ", "public", "coinbase", "td ameritrade", "edward jones", "morgan stanley", "ameriprise"} {
			if strings.Contains(inst, b) {
				return true
			}
		}
		return false
	}():
		return "investment", "brokerage"
	}
	return "depository", "checking"
}

func parseAccessURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User == nil {
		return nil, errors.New("SimpleFIN returned an invalid access URL")
	}
	if _, ok := u.User.Password(); !ok {
		return nil, errors.New("SimpleFIN returned an invalid access URL")
	}
	return u, nil
}

func (s *server) simplefinConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if decode(r, &in) != nil || len(in.Token) < 8 || len(in.Token) > 4096 {
		fail(w, 400, "Paste the setup token from SimpleFIN Bridge.")
		return
	}
	token := strings.Join(strings.Fields(in.Token), "")
	raw, e := base64.StdEncoding.DecodeString(token)
	if e != nil {
		raw, e = base64.URLEncoding.DecodeString(token)
	}
	claim, pe := url.Parse(string(raw))
	if e != nil || pe != nil || claim.Scheme != "https" || claim.Host == "" || claim.User != nil {
		fail(w, 400, "That doesn't look like a SimpleFIN setup token. Copy the whole token from SimpleFIN Bridge.")
		return
	}
	if !s.bridgeHostOK(claim.Hostname()) {
		fail(w, 400, "sofar only connects to SimpleFIN Bridge (simplefin.org). For a self-hosted bridge, add its host to SIMPLEFIN_ALLOWED_HOSTS.")
		return
	}
	req, e := http.NewRequestWithContext(r.Context(), "POST", claim.String(), nil)
	if e != nil {
		fail(w, 400, "That doesn't look like a SimpleFIN setup token.")
		return
	}
	req.Header.Set("Content-Length", "0")
	resp, e := s.client.Do(req)
	if e != nil {
		fail(w, 502, "SimpleFIN could not be reached. Try again shortly.")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	resp.Body.Close()
	if resp.StatusCode == 403 {
		fail(w, 400, "This setup token was already used or has expired. Create a new one in SimpleFIN Bridge.")
		return
	}
	if resp.StatusCode != 200 {
		fail(w, 502, fmt.Sprintf("SimpleFIN returned HTTP %d while claiming the token.", resp.StatusCode))
		return
	}
	access, e := parseAccessURL(string(body))
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	if !s.bridgeHostOK(access.Hostname()) {
		fail(w, 502, "SimpleFIN returned an access URL on an unexpected host, so sofar did not save it.")
		return
	}
	bridge := claim.Scheme + "://" + claim.Host
	if _, e = s.db.ExecContext(r.Context(), "INSERT INTO simplefin(id,access_url,bridge_url) VALUES(1,$1,$2) ON CONFLICT(id) DO UPDATE SET access_url=EXCLUDED.access_url,bridge_url=EXCLUDED.bridge_url,backfilled=false,last_fetch=NULL,last_error=NULL", s.seal(access.String()), bridge); e != nil {
		fail(w, 500, "Could not save the SimpleFIN connection.")
		return
	}
	// Accounts hidden by an earlier full disconnect come back with the new connection.
	s.db.ExecContext(r.Context(), "UPDATE accounts SET removed=false WHERE connection_id NOT IN (SELECT id FROM connections)")
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	if _, e = s.syncAll(ctx, true); e != nil {
		// The connection is saved; the first sync can be retried with "sync now".
		respond(w, map[string]any{"ok": true, "warning": e.Error()})
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *server) simplefinDisconnect(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if decode(r, &in) != nil {
		fail(w, 400, "Invalid request.")
		return
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		fail(w, 500, "Could not disconnect.")
		return
	}
	defer tx.Rollback()
	for _, q := range []string{
		"SELECT pg_advisory_xact_lock(731891)",
		"UPDATE transactions SET review_status='pending',category_id=NULL,subcategory_id=NULL,income_stream=NULL WHERE id IN (SELECT l.reimbursement_transaction_id FROM reimbursement_links l JOIN transactions t ON t.id=l.expense_transaction_id WHERE t.manual=false)",
		"DELETE FROM transactions WHERE manual=false",
		// Accounts that still hold entries you added by hand stay, hidden, so those entries survive.
		"DELETE FROM accounts WHERE NOT EXISTS (SELECT 1 FROM transactions t WHERE t.account_id=accounts.id)",
		"UPDATE accounts SET removed=true",
		"DELETE FROM connections",
		"DELETE FROM simplefin",
	} {
		if _, e = tx.ExecContext(r.Context(), q); e != nil {
			log.Error().Err(e).Msg("disconnect simplefin")
			fail(w, 500, "Could not disconnect.")
			return
		}
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "Could not disconnect.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (s *server) fetchAccounts(ctx context.Context, access *url.URL, start, end time.Time, pending bool) (sfinAccountSet, error) {
	var out sfinAccountSet
	u := *access
	u.User = nil
	u.Path = strings.TrimSuffix(u.Path, "/") + "/accounts"
	q := url.Values{"start-date": {strconv.FormatInt(start.Unix(), 10)}, "end-date": {strconv.FormatInt(end.Unix(), 10)}, "version": {"2"}}
	if pending {
		q.Set("pending", "1")
	}
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return out, e
	}
	password, _ := access.User.Password()
	req.SetBasicAuth(access.User.Username(), password)
	resp, e := s.client.Do(req)
	if e != nil {
		return out, errors.New("SimpleFIN could not be reached. Try again shortly")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 403:
		return out, errors.New("SimpleFIN no longer accepts sofar's access. Create a new setup token in SimpleFIN Bridge and connect again")
	case resp.StatusCode == 402:
		return out, errors.New("Your SimpleFIN Bridge subscription needs attention")
	case resp.StatusCode != 200:
		return out, fmt.Errorf("SimpleFIN returned HTTP %d", resp.StatusCode)
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 50<<20))
	d.UseNumber()
	if e = d.Decode(&out); e != nil {
		return out, errors.New("SimpleFIN sent a response sofar could not read")
	}
	for _, m := range out.Errors {
		out.Errlist = append(out.Errlist, sfinError{Code: "gen.", Message: m})
	}
	for i := range out.Accounts {
		if a := &out.Accounts[i]; a.ConnID == "" && a.Org != nil {
			// Protocol v1 servers describe the institution per account instead of per connection.
			a.ConnID = a.Org.Domain + a.Org.Name
			out.Connections = append(out.Connections, sfinConnection{ID: a.ConnID, Name: a.Org.Name, OrgURL: a.Org.Domain, URL: a.Org.URL})
		}
	}
	return out, nil
}

func (s *server) poll(ctx context.Context) {
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, cancel := context.WithTimeout(ctx, 10*time.Minute)
			if _, e := s.syncAll(c, false); e != nil && !errors.Is(e, errNotConnected) {
				log.Warn().Err(e).Msg("scheduled sync incomplete")
			}
			cancel()
		}
	}
}

func (s *server) syncHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	fetched, e := s.syncAll(ctx, false)
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	respond(w, map[string]bool{"ok": true, "fetched": fetched})
}

// syncAll pulls from SimpleFIN unless it was asked very recently (force skips
// that spacing, for a brand-new connection). It reports whether it fetched.
func (s *server) syncAll(ctx context.Context, force bool) (bool, error) {
	if !s.syncMu.TryLock() {
		return false, errors.New("A sync is already running. Try again in a moment")
	}
	defer s.syncMu.Unlock()
	var sealed string
	var backfilled bool
	var lastFetch sql.NullTime
	e := s.db.QueryRowContext(ctx, "SELECT access_url,backfilled,last_fetch FROM simplefin WHERE id=1").Scan(&sealed, &backfilled, &lastFetch)
	if errors.Is(e, sql.ErrNoRows) {
		return false, errNotConnected
	}
	if e != nil {
		return false, e
	}
	if !force && lastFetch.Valid && time.Since(lastFetch.Time) < minFetchInterval {
		return false, nil
	}
	raw, e := s.unseal(sealed)
	if e != nil {
		return false, errors.New("could not decrypt the SimpleFIN connection")
	}
	access, e := parseAccessURL(raw)
	if e != nil {
		return false, e
	}
	if !s.bridgeHostOK(access.Hostname()) {
		return false, errors.New("the stored SimpleFIN access points at an unexpected host; connect again")
	}
	now := time.Now()
	end := now.Add(24 * time.Hour)
	recent, e := s.fetchAccounts(ctx, access, now.Add(-recentWindow), end, true)
	if e != nil {
		s.db.ExecContext(ctx, "UPDATE simplefin SET last_error=$1,last_fetch=now() WHERE id=1", e.Error())
		return true, e
	}
	sets := []sfinAccountSet{recent}
	if !backfilled {
		stop := now.Add(-recentWindow)
		for i := 0; i < backfillChunks; i++ {
			older, e := s.fetchAccounts(ctx, access, stop.Add(-backfillChunk), stop, false)
			if e != nil {
				log.Warn().Err(e).Msg("history backfill stopped early")
				break
			}
			found := 0
			for _, a := range older.Accounts {
				found += len(a.Transactions)
			}
			if found == 0 {
				break
			}
			sets = append(sets, older)
			stop = stop.Add(-backfillChunk)
		}
	}
	if e = s.store(ctx, sets, now.Add(-recentWindow)); e != nil {
		s.db.ExecContext(ctx, "UPDATE simplefin SET last_error=$1,last_fetch=now() WHERE id=1", e.Error())
		return true, e
	}
	var failures []string
	for _, er := range recent.Errlist {
		failures = append(failures, er.Message)
	}
	status := sql.NullString{String: strings.Join(failures, " "), Valid: len(failures) > 0}
	if _, e = s.db.ExecContext(ctx, "UPDATE simplefin SET backfilled=true,last_fetch=now(),last_error=$1 WHERE id=1", status); e != nil {
		return true, e
	}
	if _, e = s.db.ExecContext(ctx, "UPDATE settings SET value=$1 WHERE key='last_sync'", now.UTC().Format(time.RFC3339)); e != nil {
		return true, e
	}
	if e = s.detect(ctx); e != nil {
		return true, e
	}
	s.notify(ctx)
	s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<now()")
	return true, nil
}

// store writes one sync's accounts and transactions atomically. sets[0] is the
// recent window (with pending items); later sets are older history.
func (s *server) store(ctx context.Context, sets []sfinAccountSet, recentStart time.Time) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(731891)"); e != nil {
		return e
	}
	recent := sets[0]
	names := map[string]string{}
	connErr := map[string]string{}
	for _, c := range recent.Connections {
		names[c.ID] = c.Name
		if _, e = tx.ExecContext(ctx, "INSERT INTO connections(id,name,org_url) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,org_url=EXCLUDED.org_url", c.ID, c.Name, c.OrgURL); e != nil {
			return e
		}
		if c.URL != "" {
			if u, pe := url.Parse(c.URL); pe == nil && u.Scheme == "https" && s.bridgeHostOK(u.Hostname()) {
				tx.ExecContext(ctx, "UPDATE simplefin SET bridge_url=$1 WHERE id=1", u.Scheme+"://"+u.Host)
			}
		}
	}
	for _, er := range recent.Errlist {
		if er.ConnID != "" && strings.HasPrefix(er.Code, "con.") {
			connErr[er.ConnID] = er.Message
		}
	}
	for id := range names {
		if _, e = tx.ExecContext(ctx, "UPDATE connections SET last_error=NULLIF($2,'') WHERE id=$1", id, connErr[id]); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE accounts SET needs_reauth=$2 WHERE connection_id=$1", id, connErr[id] != ""); e != nil {
			return e
		}
	}
	ignorePatterns := []string{}
	rows, e := tx.QueryContext(ctx, "SELECT merchant_pattern FROM ignore_rules")
	if e != nil {
		return e
	}
	for rows.Next() {
		var p string
		if e = rows.Scan(&p); e != nil {
			rows.Close()
			return e
		}
		ignorePatterns = append(ignorePatterns, p)
	}
	rows.Close()
	type known struct {
		kind    string
		removed bool
	}
	accounts := map[string]known{}
	for i, set := range sets {
		for _, a := range set.Accounts {
			if a.Currency != "" && a.Currency != "USD" {
				continue
			}
			id := accountKey(a.ConnID, a.ID)
			var existing string
			if tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE connection_id=$1 AND external_id=$2", a.ConnID, a.ID).Scan(&existing) == nil {
				id = existing
			}
			k, seen := accounts[id]
			if !seen {
				balance, be := cents(a.Balance)
				if be != nil {
					log.Warn().Err(be).Str("account", id).Msg("skipping balance")
				}
				institution := names[a.ConnID]
				if institution == "" {
					institution = "Your bank"
				}
				kind, subtype := inferType(a.Name, institution)
				// Only the recent window's balance is current; older windows just add history.
				if i == 0 {
					if e = tx.QueryRowContext(ctx, `INSERT INTO accounts(id,connection_id,external_id,institution_name,account_type,account_subtype,display_name,balance,last_synced_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())
						ON CONFLICT(id) DO UPDATE SET institution_name=EXCLUDED.institution_name,display_name=EXCLUDED.display_name,balance=EXCLUDED.balance,last_synced_at=now(),needs_reauth=$9
						RETURNING account_type,removed`, id, a.ConnID, a.ID, institution, kind, subtype, a.Name, balance, connErr[a.ConnID] != "").Scan(&k.kind, &k.removed); e != nil {
						return e
					}
				} else if e = tx.QueryRowContext(ctx, "SELECT account_type,removed FROM accounts WHERE id=$1", id).Scan(&k.kind, &k.removed); e != nil {
					continue
				}
				accounts[id] = k
			}
			// Removed accounts stay hidden; investment accounts only track balances.
			if k.removed || k.kind == "investment" {
				continue
			}
			seenIDs := map[string]bool{}
			for _, t := range a.Transactions {
				tid := id + ":" + t.ID
				seenIDs[tid] = true
				if e = upsertTransaction(ctx, tx, id, tid, t, ignorePatterns); e != nil {
					return e
				}
			}
			if i == 0 {
				// A pending item that disappeared either posted under a new id or was dropped.
				rows, e := tx.QueryContext(ctx, "SELECT id FROM transactions WHERE account_id=$1 AND bank_pending=true AND date>=$2", id, recentStart.Format("2006-01-02"))
				if e != nil {
					return e
				}
				var stale []string
				for rows.Next() {
					var tid string
					if e = rows.Scan(&tid); e != nil {
						rows.Close()
						return e
					}
					if !seenIDs[tid] {
						stale = append(stale, tid)
					}
				}
				rows.Close()
				for _, tid := range stale {
					if e = removeTransaction(ctx, tx, tid); e != nil {
						return e
					}
				}
			}
		}
	}
	return tx.Commit()
}

func upsertTransaction(ctx context.Context, tx *sql.Tx, accountID, id string, t sfinTransaction, ignorePatterns []string) error {
	amount, e := cents(t.Amount)
	if e != nil {
		log.Warn().Err(e).Str("transaction", id).Msg("skipping transaction")
		return nil
	}
	direction := "out"
	if amount > 0 {
		direction = "in"
	}
	if amount < 0 {
		amount = -amount
	}
	when := unix(t.TransactedAt)
	if when <= 0 {
		when = unix(t.Posted)
	}
	if when <= 0 {
		when = time.Now().Unix()
	}
	date := time.Unix(when, 0).In(time.Local).Format("2006-01-02")
	pending := t.Pending || unix(t.Posted) == 0
	description := strings.TrimSpace(t.Description)
	merchant := strings.TrimSpace(t.Payee)
	if merchant == "" {
		merchant = description
	}
	merchant = budget.Prettify(merchant)
	if merchant == "" {
		merchant = "Unknown merchant"
	}
	if len(merchant) > 200 {
		merchant = merchant[:200]
	}
	if len(description) > 500 {
		description = description[:500]
	}
	// A changed amount or direction invalidates earlier netting and splits.
	var oldAmount int64
	var oldDirection string
	err := tx.QueryRowContext(ctx, "SELECT amount,direction FROM transactions WHERE id=$1", id).Scan(&oldAmount, &oldDirection)
	changed := err == nil && (oldAmount != amount || oldDirection != direction)
	if changed {
		if e = unlinkForTransaction(ctx, tx, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM transaction_splits WHERE transaction_id=$1", id); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction,bank_pending,description) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$10) ON CONFLICT(id) DO UPDATE SET date=$3,amount=$4,raw_merchant=$5,clean_merchant=$6,direction=$7,bank_pending=$8,description=$10,review_status=CASE WHEN $9 THEN 'pending' ELSE transactions.review_status END,category_id=CASE WHEN $9 THEN NULL ELSE transactions.category_id END,subcategory_id=CASE WHEN $9 THEN NULL ELSE transactions.subcategory_id END,income_stream=CASE WHEN $9 THEN NULL ELSE transactions.income_stream END,ignored=CASE WHEN $9 THEN false ELSE transactions.ignored END,ignore_reason=CASE WHEN $9 THEN '' ELSE transactions.ignore_reason END`, id, accountID, date, amount, merchant, budget.Normalize(merchant), direction, pending, changed, description)
	if e != nil {
		return e
	}
	if errors.Is(err, sql.ErrNoRows) && !pending && direction == "out" {
		for _, pattern := range ignorePatterns {
			if budget.Matches(merchant, pattern) {
				_, e = tx.ExecContext(ctx, "UPDATE transactions SET review_status='confirmed',ignored=true,ignore_reason='always ignored',category_id=NULL WHERE id=$1", id)
				return e
			}
		}
	}
	return nil
}

func unlinkForTransaction(ctx context.Context, tx *sql.Tx, id string) error {
	if _, e := tx.ExecContext(ctx, "UPDATE transactions SET review_status='pending',category_id=NULL,subcategory_id=NULL,income_stream=NULL WHERE id IN (SELECT reimbursement_transaction_id FROM reimbursement_links WHERE expense_transaction_id=$1 OR reimbursement_transaction_id=$1)", id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "DELETE FROM reimbursement_links WHERE expense_transaction_id=$1 OR reimbursement_transaction_id=$1", id)
	return e
}
func removeTransaction(ctx context.Context, tx *sql.Tx, id string) error {
	if e := unlinkForTransaction(ctx, tx, id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "DELETE FROM transactions WHERE id=$1", id)
	return e
}

var monthlyByNature = map[string]bool{"subscriptions": true, "phone": true, "utilities": true, "insurance": true, "housing": true, "loans": true, "fitness": true}

// detect groups similar confirmed-or-pending transactions into recurring candidates.
func (s *server) detect(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(731891)"); e != nil {
		return e
	}
	st, e := readState(ctx, tx)
	if e != nil {
		return e
	}
	groups := map[string][]budget.Transaction{}
	for _, t := range st.Transactions {
		if t.BankPending || t.IncomeStream == "transfer" || t.Ignored || t.Manual {
			continue
		}
		linked := false
		for _, l := range st.Links {
			if l.CreditID == t.ID {
				linked = true
			}
		}
		if linked {
			continue
		}
		pattern := budget.Normalize(t.Merchant)
		if len(pattern) < 3 {
			continue
		}
		key := t.Direction + ":" + pattern
		for existing, ts := range groups {
			if ts[0].Direction == t.Direction && budget.Matches(ts[0].Merchant, t.Merchant) {
				key = existing
				break
			}
		}
		groups[key] = append(groups[key], t)
	}
	for _, ts := range groups {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Date < ts[j].Date })
		kind := "bill"
		if ts[0].Direction == "in" {
			kind = "income"
		}
		var existing *budget.Recurring
		for i := range st.Recurring {
			r := &st.Recurring[i]
			if r.Type == kind && budget.Matches(r.Merchant, ts[0].Merchant) {
				existing = r
				break
			}
		}
		if existing != nil {
			if existing.Dismissed {
				continue
			}
			latest := ts[len(ts)-1]
			if math.Abs(float64(latest.Amount-existing.Amount)) > float64(existing.Amount)*existing.Tolerance/100 {
				if _, e = tx.ExecContext(ctx, "UPDATE recurring_groups SET mismatch=true WHERE id=$1", existing.ID); e != nil {
					return e
				}
			}
			for _, t := range ts {
				if _, e = tx.ExecContext(ctx, "UPDATE transactions SET is_recurring=true,recurring_group_id=$1 WHERE id=$2", existing.ID, t.ID); e != nil {
					return e
				}
			}
			continue
		}
		if len(ts) < st.Threshold {
			// Merchants that bill monthly by nature are worth suggesting after one recent charge.
			latest := ts[len(ts)-1]
			sub, _, _ := budget.LookupMerchant(latest.Merchant)
			d, err := time.Parse("2006-01-02", latest.Date)
			if kind != "bill" || !monthlyByNature[sub] || err != nil || time.Since(d) > 40*24*time.Hour {
				continue
			}
			category := budget.GroupOf(sub)
			if latest.Category != nil {
				category = *latest.Category
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO recurring_groups(id,merchant_pattern,expected_amount,cadence,type,occurrence_threshold_at_creation,category_id,next_date) VALUES($1,$2,$3,'monthly','bill',$4,$5,$6) ON CONFLICT(merchant_pattern,type) DO NOTHING`, randomID(), budget.Normalize(latest.Merchant), latest.Amount, st.Threshold, category, d.AddDate(0, 1, 0)); e != nil {
				return e
			}
			for _, t := range ts {
				if _, e = tx.ExecContext(ctx, "UPDATE transactions SET is_recurring=true,recurring_group_id=(SELECT id FROM recurring_groups WHERE merchant_pattern=$1 AND type='bill') WHERE id=$2", budget.Normalize(latest.Merchant), t.ID); e != nil {
					return e
				}
			}
			continue
		}
		recent := ts[len(ts)-st.Threshold:]
		dates := []time.Time{}
		var total int64
		for _, t := range recent {
			d, err := time.Parse("2006-01-02", t.Date)
			if err != nil {
				return err
			}
			dates = append(dates, d)
			total += t.Amount
		}
		cadence := budget.Cadence(dates)
		if cadence == "" {
			continue
		}
		amount := total / int64(len(recent))
		consistent := true
		for _, t := range recent {
			if math.Abs(float64(t.Amount-amount)) > float64(amount)*.2 {
				consistent = false
			}
		}
		if !consistent {
			continue
		}
		latest := recent[len(recent)-1]
		next := dates[len(dates)-1]
		switch cadence {
		case "weekly":
			next = next.AddDate(0, 0, 7)
		case "biweekly":
			next = next.AddDate(0, 0, 14)
		case "monthly":
			next = next.AddDate(0, 1, 0)
		case "annual":
			next = next.AddDate(1, 0, 0)
		}
		category := "expenses"
		stream := ""
		if latest.Category != nil {
			category = *latest.Category
		}
		if kind == "income" {
			stream = latest.IncomeStream
		}
		id := randomID()
		_, e = tx.ExecContext(ctx, `INSERT INTO recurring_groups(id,merchant_pattern,expected_amount,cadence,type,income_stream,occurrence_threshold_at_creation,category_id,next_date) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9) ON CONFLICT(merchant_pattern,type) DO NOTHING`, id, budget.Normalize(latest.Merchant), amount, cadence, kind, stream, st.Threshold, category, next)
		if e != nil {
			return e
		}
		for _, t := range ts {
			if _, e = tx.ExecContext(ctx, "UPDATE transactions SET is_recurring=true,recurring_group_id=(SELECT id FROM recurring_groups WHERE merchant_pattern=$1 AND type=$2) WHERE id=$3", budget.Normalize(latest.Merchant), kind, t.ID); e != nil {
				return e
			}
		}
	}
	return tx.Commit()
}

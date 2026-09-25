package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type plaidError struct {
	Code    string `json:"error_code"`
	Message string `json:"error_message"`
}

func (e plaidError) Error() string { return "Plaid: " + e.Code }
func (s *server) plaid(ctx context.Context, path string, in map[string]any, out any) error {
	if os.Getenv("PLAID_CLIENT_ID") == "" || os.Getenv("PLAID_SECRET") == "" {
		return errors.New("Plaid is not configured. Add PLAID_CLIENT_ID and PLAID_SECRET on your server")
	}
	body, e := json.Marshal(in)
	if e != nil {
		return e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", s.plaidBase+path, bytes.NewReader(body))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("PLAID-CLIENT-ID", os.Getenv("PLAID_CLIENT_ID"))
	r.Header.Set("PLAID-SECRET", os.Getenv("PLAID_SECRET"))
	r.Header.Set("Plaid-Version", "2020-09-14")
	resp, e := s.client.Do(r)
	if e != nil {
		return errors.New("Plaid could not be reached. Try again shortly")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var p plaidError
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p) != nil {
			return fmt.Errorf("Plaid returned HTTP %d", resp.StatusCode)
		}
		return p
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(out)
}
func (s *server) linkToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
	}
	if decode(r, &in) != nil {
		fail(w, 400, "Invalid connection request.")
		return
	}
	if in.Kind != "transactions" && in.Kind != "investments" {
		fail(w, 400, "Choose a bank or investment account.")
		return
	}
	req := map[string]any{"client_name": "sofar", "language": "en", "country_codes": []string{"US"}, "user": map[string]string{"client_user_id": "sofar-single-user"}, "products": []string{in.Kind}}
	if in.Kind == "transactions" {
		req["transactions"] = map[string]int{"days_requested": 730}
		req["optional_products"] = []string{"investments"}
		req["account_filters"] = map[string]any{"depository": map[string]any{"account_subtypes": []string{"checking", "savings"}}}
	} else {
		req["account_filters"] = map[string]any{"investment": map[string]any{"account_subtypes": []string{"all"}}}
	}
	if strings.HasPrefix(s.origin, "https://") {
		req["webhook"] = s.origin + "/api/plaid/webhook"
	}
	var out map[string]any
	if e := s.plaid(r.Context(), "/link/token/create", req, &out); e != nil {
		fail(w, 502, e.Error())
		return
	}
	respond(w, map[string]any{"link_token": out["link_token"]})
}
func (s *server) exchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicToken string `json:"public_token"`
		Institution string `json:"institution"`
		Kind        string `json:"kind"`
	}
	if decode(r, &in) != nil || in.PublicToken == "" || (in.Kind != "transactions" && in.Kind != "investments") || len(in.Institution) > 200 {
		fail(w, 400, "Invalid bank connection.")
		return
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ItemID      string `json:"item_id"`
	}
	if e := s.plaid(r.Context(), "/item/public_token/exchange", map[string]any{"public_token": in.PublicToken}, &out); e != nil {
		fail(w, 502, e.Error())
		return
	}
	if _, e := s.db.ExecContext(r.Context(), "INSERT INTO plaid_items(id,access_token,institution_name,kind) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET access_token=EXCLUDED.access_token", out.ItemID, s.seal(out.AccessToken), in.Institution, in.Kind); e != nil {
		fail(w, 500, "Could not save the bank connection.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}

type plaidAccount struct {
	ID       string `json:"account_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	Mask     string `json:"mask"`
	Balances struct {
		Current  *float64 `json:"current"`
		Currency string   `json:"iso_currency_code"`
	} `json:"balances"`
}
type plaidTransaction struct {
	ID        string  `json:"transaction_id"`
	AccountID string  `json:"account_id"`
	Date      string  `json:"date"`
	Amount    float64 `json:"amount"`
	Name      string  `json:"name"`
	Merchant  string  `json:"merchant_name"`
	Pending   bool    `json:"pending"`
	PendingID string  `json:"pending_transaction_id"`
	Currency  string  `json:"iso_currency_code"`
}
type syncPage struct {
	Added    []plaidTransaction `json:"added"`
	Modified []plaidTransaction `json:"modified"`
	Removed  []struct {
		ID string `json:"transaction_id"`
	} `json:"removed"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}
type plaidItem struct{ ID, Token, Cursor, Institution, Kind string }

func (s *server) poll(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, cancel := context.WithTimeout(ctx, 10*time.Minute)
			if e := s.syncAll(c); e != nil {
				log.Warn().Err(e).Msg("hourly sync incomplete")
			}
			cancel()
		}
	}
}
func (s *server) syncHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	if e := s.syncAll(ctx); e != nil {
		fail(w, 502, e.Error())
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) syncAll(ctx context.Context) error {
	if !s.syncMu.TryLock() {
		return errors.New("A sync is already running. Try again in a moment")
	}
	defer s.syncMu.Unlock()
	rows, e := s.db.QueryContext(ctx, "SELECT id,access_token,cursor,institution_name,kind FROM plaid_items")
	if e != nil {
		return e
	}
	items := []plaidItem{}
	for rows.Next() {
		var it plaidItem
		if e = rows.Scan(&it.ID, &it.Token, &it.Cursor, &it.Institution, &it.Kind); e != nil {
			rows.Close()
			return e
		}
		items = append(items, it)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var failures []string
	for _, it := range items {
		if e = s.syncItem(ctx, it); e != nil {
			failures = append(failures, it.Institution+": "+e.Error())
			s.db.ExecContext(ctx, "UPDATE plaid_items SET last_error=$1 WHERE id=$2", e.Error(), it.ID)
			log.Warn().Str("item", it.ID).Err(e).Msg("sync incomplete")
		}
	}
	if e = s.detect(ctx); e != nil {
		return e
	}
	if len(failures) == 0 {
		_, e = s.db.ExecContext(ctx, "UPDATE settings SET value=$1 WHERE key='last_sync'", time.Now().UTC().Format(time.RFC3339))
		if e != nil {
			return e
		}
	}
	s.notify(ctx)
	s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<now()")
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
func allowedAccount(a plaidAccount) bool {
	return a.Type == "investment" || (a.Type == "depository" && (a.Subtype == "checking" || a.Subtype == "savings"))
}
func (s *server) syncItem(ctx context.Context, it plaidItem) error {
	token, e := s.unseal(it.Token)
	if e != nil {
		return errors.New("could not decrypt bank token")
	}
	var acct struct {
		Accounts []plaidAccount `json:"accounts"`
	}
	if e = s.plaid(ctx, "/accounts/get", map[string]any{"access_token": token}, &acct); e != nil {
		return e
	}
	hasInvestment := false
	for _, a := range acct.Accounts {
		if a.Type == "investment" {
			hasInvestment = true
		}
	}
	if hasInvestment {
		var holdings struct {
			Accounts []plaidAccount `json:"accounts"`
		}
		if err := s.plaid(ctx, "/investments/holdings/get", map[string]any{"access_token": token}, &holdings); err == nil {
			for i, a := range acct.Accounts {
				for _, h := range holdings.Accounts {
					if a.ID == h.ID {
						acct.Accounts[i] = h
					}
				}
			}
		} else {
			log.Info().Str("item", it.ID).Msg("holdings unavailable; using account balance")
		}
	}
	added := []plaidTransaction{}
	removed := []string{}
	cursor := it.Cursor
	if it.Kind == "transactions" {
		completed := false
		for retry := 0; retry < 3 && !completed; retry++ {
			added = nil
			removed = nil
			cursor = it.Cursor
			for page := 0; page < 200; page++ {
				var out syncPage
				e = s.plaid(ctx, "/transactions/sync", map[string]any{"access_token": token, "cursor": cursor, "count": 500}, &out)
				if e != nil {
					var pe plaidError
					if errors.As(e, &pe) && pe.Code == "TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION" {
						break
					}
					return e
				}
				added = append(added, out.Added...)
				added = append(added, out.Modified...)
				for _, x := range out.Removed {
					removed = append(removed, x.ID)
				}
				cursor = out.NextCursor
				if !out.HasMore {
					completed = true
					break
				}
			}
		}
		if !completed {
			return errors.New("transaction history changed during pagination; will retry on next sync")
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(731891)"); e != nil {
		return e
	}
	allowed := map[string]bool{}
	for _, a := range acct.Accounts {
		if !allowedAccount(a) {
			continue
		}
		if a.Balances.Currency != "USD" && a.Balances.Currency != "" {
			continue
		}
		allowed[a.ID] = true
		var balance int64
		if a.Balances.Current != nil {
			balance = int64(math.Round(*a.Balances.Current * 100))
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,plaid_item_id,plaid_account_id,institution_name,account_type,account_subtype,display_name,mask,balance,last_synced_at) VALUES($1,$2,$1,$3,$4,$5,$6,$7,$8,now()) ON CONFLICT(id) DO UPDATE SET display_name=$6,mask=$7,balance=$8,last_synced_at=now()`, a.ID, it.ID, it.Institution, a.Type, a.Subtype, a.Name, a.Mask, balance)
		if e != nil {
			return e
		}
	}
	for _, t := range added {
		if !allowed[t.AccountID] || t.Currency != "USD" && t.Currency != "" {
			continue
		}
		direction := "out"
		if t.Amount < 0 {
			direction = "in"
		}
		amount := int64(math.Round(math.Abs(t.Amount) * 100))
		merchant := t.Merchant
		if merchant == "" {
			merchant = t.Name
		}
		if merchant == "" {
			merchant = "Unknown merchant"
		}
		if t.PendingID != "" {
			if e = removeTransaction(ctx, tx, t.PendingID); e != nil {
				return e
			}
		}
		// Modified money/direction must be reviewed again; invalidate netting before updating it.
		var oldAmount int64
		var oldDirection string
		err := tx.QueryRowContext(ctx, "SELECT amount,direction FROM transactions WHERE id=$1", t.ID).Scan(&oldAmount, &oldDirection)
		changed := err == nil && (oldAmount != amount || oldDirection != direction)
		if changed {
			if e = unlinkForTransaction(ctx, tx, t.ID); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO transactions(id,account_id,date,amount,raw_merchant,clean_merchant,direction,bank_pending) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET date=$3,amount=$4,raw_merchant=$5,clean_merchant=$6,direction=$7,bank_pending=$8,review_status=CASE WHEN $9 THEN 'pending' ELSE transactions.review_status END,category_id=CASE WHEN $9 THEN NULL ELSE transactions.category_id END,subcategory_id=CASE WHEN $9 THEN NULL ELSE transactions.subcategory_id END,income_stream=CASE WHEN $9 THEN NULL ELSE transactions.income_stream END`, t.ID, t.AccountID, t.Date, amount, merchant, budget.Normalize(merchant), direction, t.Pending, changed)
		if e != nil {
			return e
		}
	}
	for _, id := range removed {
		if e = removeTransaction(ctx, tx, id); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE plaid_items SET cursor=$1,last_error=NULL WHERE id=$2", cursor, it.ID); e != nil {
		return e
	}
	return tx.Commit()
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
		if t.BankPending || t.IncomeStream == "transfer" {
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
func (s *server) webhook(w http.ResponseWriter, r *http.Request) {
	body, e := io.ReadAll(r.Body)
	if e != nil {
		fail(w, 400, "Invalid webhook.")
		return
	}
	if e = s.verifyWebhook(r.Context(), r.Header.Get("Plaid-Verification"), body); e != nil {
		fail(w, 401, "Invalid webhook signature.")
		return
	}
	var payload struct {
		Code   string `json:"webhook_code"`
		ItemID string `json:"item_id"`
	}
	if json.Unmarshal(body, &payload) != nil {
		fail(w, 400, "Invalid webhook.")
		return
	}
	var exists bool
	if e = s.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM plaid_items WHERE id=$1)", payload.ItemID).Scan(&exists); e != nil || !exists {
		fail(w, 400, "Unknown item.")
		return
	}
	switch payload.Code {
	case "SYNC_UPDATES_AVAILABLE", "HISTORICAL_UPDATE", "DEFAULT_UPDATE", "HOLDINGS_UPDATED":
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		if e = s.syncAll(ctx); e != nil {
			fail(w, 503, "Sync incomplete; retry webhook.")
			return
		}
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) verifyWebhook(ctx context.Context, raw string, body []byte) error {
	claims := jwt.MapClaims{}
	token, e := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 200 {
			return nil, errors.New("invalid key id")
		}
		var out struct {
			Key struct {
				X, Y, Alg, Crv, Kty string
				ExpiredAt           *int64 `json:"expired_at"`
			} `json:"key"`
		}
		if e := s.plaid(ctx, "/webhook_verification_key/get", map[string]any{"key_id": kid}, &out); e != nil {
			return nil, e
		}
		if out.Key.Alg != "ES256" || out.Key.Crv != "P-256" || out.Key.Kty != "EC" || out.Key.ExpiredAt != nil {
			return nil, errors.New("invalid key")
		}
		x, e := base64.RawURLEncoding.DecodeString(out.Key.X)
		if e != nil {
			return nil, e
		}
		y, e := base64.RawURLEncoding.DecodeString(out.Key.Y)
		if e != nil {
			return nil, e
		}
		key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !key.Curve.IsOnCurve(key.X, key.Y) {
			return nil, errors.New("invalid curve point")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuedAt())
	if e != nil || !token.Valid {
		return errors.New("invalid signature")
	}
	issued, e := claims.GetIssuedAt()
	if e != nil || issued == nil || time.Since(issued.Time) > 5*time.Minute || issued.Time.After(time.Now().Add(5*time.Second)) {
		return errors.New("expired signature")
	}
	expected, ok := claims["request_body_sha256"].(string)
	actual := sha256.Sum256(body)
	if !ok || subtle.ConstantTimeCompare([]byte(expected), []byte(hex.EncodeToString(actual[:]))) != 1 {
		return errors.New("body hash mismatch")
	}
	return nil
}

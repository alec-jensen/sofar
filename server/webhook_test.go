package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookVerificationRejectsTamperingAndStaleEvents(t *testing.T) {
	t.Setenv("PLAID_CLIENT_ID", "fixture")
	t.Setenv("PLAID_SECRET", "fixture")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook_verification_key/get" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"key": map[string]any{"alg": "ES256", "crv": "P-256", "kty": "EC", "x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}})
	}))
	defer api.Close()
	s := server{client: api.Client(), plaidBase: api.URL}
	body := []byte(`{"webhook_code":"SYNC_UPDATES_AVAILABLE","item_id":"fixture"}`)
	digest := sha256.Sum256(body)
	sign := func(at time.Time) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iat": at.Unix(), "request_body_sha256": hex.EncodeToString(digest[:])})
		token.Header["kid"] = "fixture"
		raw, e := token.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	valid := sign(time.Now())
	if err = s.verifyWebhook(context.Background(), valid, body); err != nil {
		t.Fatal(err)
	}
	if s.verifyWebhook(context.Background(), valid, []byte(`{"tampered":true}`)) == nil {
		t.Fatal("tampered body accepted")
	}
	if s.verifyWebhook(context.Background(), sign(time.Now().Add(-6*time.Minute)), body) == nil {
		t.Fatal("stale webhook accepted")
	}
	if s.verifyWebhook(context.Background(), sign(time.Now().Add(time.Minute)), body) == nil {
		t.Fatal("future webhook accepted")
	}
	if s.verifyWebhook(context.Background(), "invalid", body) == nil {
		t.Fatal("malformed signature accepted")
	}
}

package logging

import (
	"bytes"
	"errors"
	"github.com/alec-jensen/sofar/internal/money"
	"strings"
	"testing"
)

func TestS17_AccessURLNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "debug")
	access := "https://user123:s3cretpass@beta-bridge.simplefin.org/simplefin"
	type conn struct{ URL string }
	log.Info().Str("url", access).Interface("conn", conn{URL: access}).Err(errors.New("get " + access + ": timeout")).Msg("pulled " + access)
	out := buf.String()
	for _, leak := range []string{"user123", "s3cretpass", "beta-bridge.simplefin.org/simplefin"} {
		if strings.Contains(out, leak) {
			t.Errorf("log output leaks %q: %s", leak, out)
		}
	}
}

func TestS17_AuthorizationHeaderNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	lg := New(&buf, "info")
	lg.Info().Str("h", "Authorization: Bearer abcdefghijklmnop1234").Msg("x")
	if strings.Contains(buf.String(), "abcdefghijklmnop1234") {
		t.Error("bearer token leaked")
	}
}

func TestLogging_DescriptionsRedacted(t *testing.T) {
	d := Desc("SQ *BLUE BOTTLE COFFEE #12")
	if strings.Contains(d, "BLUE") || !strings.Contains(d, "/") {
		t.Errorf("Desc leaks text or has the wrong shape: %s", d)
	}
	if Desc("a") == Desc("b") {
		t.Error("different text should hash differently")
	}
	if got := Acct("1234567890"); got != "...90" {
		t.Errorf("Acct = %s", got)
	}
	if Acct("x") != "..**" {
		t.Error("tiny ids are fully masked")
	}
	for c, want := range map[int64]string{500: "<$10", -5_000: "$10-100", 50_000: "$100-1k", 250_000: ">=$1k"} {
		if got := Amount(moneyOf(c)); got != want {
			t.Errorf("Amount(%d) = %s, want %s", c, got, want)
		}
	}
}

func moneyOf(c int64) money.Cents { return money.Cents(c) }

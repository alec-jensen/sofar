package crypt

import (
	"strings"
	"testing"
)

func TestS17_SealRoundTripAndTamper(t *testing.T) {
	b, err := New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed := b.Seal("https://user:pass@bridge.example/simplefin")
	if strings.Contains(sealed, "pass") {
		t.Fatal("ciphertext leaks the secret")
	}
	if got, err := b.Unseal(sealed); err != nil || got != "https://user:pass@bridge.example/simplefin" {
		t.Fatalf("round trip failed: %q %v", got, err)
	}
	if _, err := b.Unseal(sealed[:len(sealed)-4] + "AAAA"); err == nil {
		t.Fatal("a tampered value must not open")
	}
	if _, err := b.Unseal("not base64!"); err == nil {
		t.Fatal("garbage must not open")
	}
	other, _ := New([]byte(strings.Repeat("k", 32)))
	if _, err := other.Unseal(sealed); err == nil {
		t.Fatal("a different key must not open it")
	}
}

func TestCrypt_RejectsWrongKeyLength(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("16-byte key accepted")
	}
}

func TestS17_MACIsKeyedAndPurposeBound(t *testing.T) {
	a, _ := New(make([]byte, 32))
	b, _ := New([]byte(strings.Repeat("k", 32)))
	if a.MAC("recovery", "x") == b.MAC("recovery", "x") {
		t.Fatal("the key must change the MAC")
	}
	if a.MAC("recovery", "x") == a.MAC("session", "x") {
		t.Fatal("the purpose must change the MAC")
	}
}

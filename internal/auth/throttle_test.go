package auth

import (
	"testing"
	"time"
)

func TestS19_19_LockoutBackoffNeverLocksOwnerFromAnotherAddress(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	th := NewThrottle()
	th.Now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if !th.Allow("198.51.100.1") {
			t.Fatalf("attempt %d from the attacker should still be allowed", i+1)
		}
	}
	if th.Allow("198.51.100.1") {
		t.Fatal("the attacker should be locked out after ten attempts")
	}
	if !th.Allow("198.51.100.2") {
		t.Fatal("the owner on another address must not be locked out by the attacker")
	}
	th.Succeeded("198.51.100.1")
	if !th.Allow("198.51.100.1") {
		t.Fatal("a successful login clears that caller's attempts")
	}
	now = now.Add(16 * time.Minute)
	for i := 0; i < 10; i++ {
		if !th.Allow("203.0.113.9") {
			t.Fatal("the window resets after 15 minutes")
		}
	}
}

func TestS19_19_GlobalSlowdownDelaysOnlyFailures(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	th := NewThrottle()
	th.Now = func() time.Time { return now }
	for i := 0; i < 20; i++ {
		if d := th.FailDelay(); d != 0 {
			t.Fatalf("failure %d should not be delayed yet, got %v", i+1, d)
		}
	}
	if d := th.FailDelay(); d != 250*time.Millisecond {
		t.Errorf("21st failure delay = %v", d)
	}
	for i := 0; i < 100; i++ {
		th.FailDelay()
	}
	if d := th.FailDelay(); d != 3*time.Second {
		t.Errorf("delay should cap at 3s, got %v", d)
	}
	now = now.Add(16 * time.Minute)
	if d := th.FailDelay(); d != 0 {
		t.Errorf("slowdown clears after the window, got %v", d)
	}
}

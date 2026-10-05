package auth

import (
	"sync"
	"time"
)

// Throttle slows guessing without ever locking the owner out. Each caller
// address gets a limited number of attempts per window (successes clear it),
// and once many failures pile up across all addresses, failures (never correct
// logins) are delayed, so a distributed guesser is throttled too. spec: S19.19.
type Throttle struct {
	mu          sync.Mutex
	attempts    map[string]attempt
	globalFails int
	globalUntil time.Time

	Now       func() time.Time
	Sleep     func(time.Duration)
	PerWindow int           // attempts per address per window (default 10)
	Window    time.Duration // default 15 minutes
}

type attempt struct {
	count int
	until time.Time
}

// NewThrottle returns a Throttle with production settings.
func NewThrottle() *Throttle {
	return &Throttle{attempts: map[string]attempt{}, Now: time.Now, Sleep: time.Sleep, PerWindow: 10, Window: 15 * time.Minute}
}

// Allow records an attempt from ip and reports whether it may proceed.
func (t *Throttle) Allow(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	for k, a := range t.attempts {
		if now.After(a.until) {
			delete(t.attempts, k)
		}
	}
	if len(t.attempts) > 20000 {
		// Bound memory under a flood of distinct addresses; windows are short anyway.
		t.attempts = map[string]attempt{}
	}
	a := t.attempts[ip]
	if a.count >= t.PerWindow {
		return false
	}
	if a.count == 0 {
		a.until = now.Add(t.Window)
	}
	a.count++
	t.attempts[ip] = a
	return true
}

// Succeeded forgets failed attempts from this address.
func (t *Throttle) Succeeded(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, ip)
}

// FailDelay returns how long a failure should be delayed, and counts it.
func (t *Throttle) FailDelay() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	if now.After(t.globalUntil) {
		t.globalFails = 0
		t.globalUntil = now.Add(t.Window)
	}
	t.globalFails++
	over := t.globalFails - 2*t.PerWindow
	if over <= 0 {
		return 0
	}
	d := time.Duration(over) * 250 * time.Millisecond
	if d > 3*time.Second {
		d = 3 * time.Second
	}
	return d
}

// Failed counts a failure and sleeps for the slowdown, if any.
func (t *Throttle) Failed() {
	if d := t.FailDelay(); d > 0 {
		t.Sleep(d)
	}
}

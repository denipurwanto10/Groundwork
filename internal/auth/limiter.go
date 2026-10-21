package auth

import (
	"sync"
	"time"
)

// Defaults of the sign-in limiter: five wrong passwords in five minutes lock
// that client out for five minutes.
const (
	defaultLoginAttempts = 5
	defaultLoginWindow   = 5 * time.Minute
	defaultLoginCooldown = 5 * time.Minute
)

// loginLimiter counts the failed sign-in attempts of every client.
//
// It is a small in-memory guard against password guessing. The state is lost
// when the server restarts, which is fine for a local tool: the limit is a
// speed bump, not a security boundary.
type loginLimiter struct {
	mu       sync.Mutex
	attempts int
	window   time.Duration
	cooldown time.Duration
	now      func() time.Time

	// failures holds the clients that are currently counting failures.
	failures map[string]*loginFailure
}

type loginFailure struct {
	// count is the number of failures inside the window.
	count int
	// first is when the current window started.
	first time.Time
	// lockedUntil is when the client may try again.
	lockedUntil time.Time
}

// newLoginLimiter creates a limiter. The window is the time the failures are
// counted in, the cooldown how long a client stays locked out.
func newLoginLimiter(attempts int, window, cooldown time.Duration) *loginLimiter {
	if attempts < 1 {
		attempts = 1
	}
	if window <= 0 {
		window = defaultLoginWindow
	}
	if cooldown <= 0 {
		cooldown = defaultLoginCooldown
	}
	return &loginLimiter{
		attempts: attempts,
		window:   window,
		cooldown: cooldown,
		now:      time.Now,
		failures: make(map[string]*loginFailure),
	}
}

// allow reports whether the client may try to sign in.
func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, ok := l.failures[key]
	if !ok {
		return true
	}

	now := l.now()
	if !f.lockedUntil.IsZero() && now.Before(f.lockedUntil) {
		return false
	}
	// The lock is over: the client starts with a clean slate.
	if !f.lockedUntil.IsZero() {
		delete(l.failures, key)
		return true
	}
	// The window is over: the old failures do not count any more.
	if now.Sub(f.first) > l.window {
		delete(l.failures, key)
	}
	return true
}

// fail records one failed attempt and locks the client out when it has used
// up its allowance.
func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	f, ok := l.failures[key]
	if !ok || now.Sub(f.first) > l.window {
		f = &loginFailure{first: now}
		l.failures[key] = f
	}
	f.count++
	if f.count >= l.attempts {
		f.lockedUntil = now.Add(l.cooldown)
	}
}

// reset forgets the failures of a client that signed in successfully.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

package auth

import (
	"testing"
	"time"
)

func TestRefreshTokenIsNotAnAccessToken(t *testing.T) {
	svc := newTestService()
	user := User{ID: 7, Username: "crew", Email: "crew@groundwork.local", Role: RoleViewer}

	refresh, err := svc.IssueRefreshToken(user)
	if err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}

	// A refresh token must not open the API on its own.
	if _, err := svc.ParseToken(refresh); err == nil {
		t.Fatal("expected a refresh token to be refused as an access token")
	}

	claims, err := svc.ParseRefreshToken(refresh)
	if err != nil {
		t.Fatalf("parse refresh token: %v", err)
	}
	if claims.Subject != "7" || claims.Username != "crew" {
		t.Fatalf("unexpected claims %+v", claims)
	}
}

func TestAccessTokenIsNotARefreshToken(t *testing.T) {
	svc := newTestService()
	access, err := svc.IssueToken(User{ID: 7, Username: "crew", Role: RoleViewer})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if _, err := svc.ParseRefreshToken(access); err == nil {
		t.Fatal("expected an access token to be refused as a refresh token")
	}
}

func TestNewSessionCarriesBothTokens(t *testing.T) {
	svc := newTestService()
	session, err := svc.NewSession(User{ID: 3, Username: "admin", Role: RoleAdmin})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("a session needs an access token and a refresh token")
	}
	if session.AccessToken == session.RefreshToken {
		t.Fatal("the two tokens must differ")
	}
	if session.ExpiresIn != svc.ttl {
		t.Fatalf("expires in = %s, want %s", session.ExpiresIn, svc.ttl)
	}
}

func TestLoginLimiterLocksAfterTooManyFailures(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute, 5*time.Minute)

	key := limiterKey("127.0.0.1", "admin")
	for i := 0; i < 3; i++ {
		if !limiter.allow(key) {
			t.Fatalf("attempt %d: the client should still be allowed", i+1)
		}
		limiter.fail(key)
	}

	if limiter.allow(key) {
		t.Fatal("expected the client to be locked out after three failures")
	}

	// Another client is not affected by the first one's failures.
	if !limiter.allow(limiterKey("10.0.0.5", "admin")) {
		t.Fatal("a different client must not be locked out")
	}
}

func TestLoginLimiterForgetsAfterASuccess(t *testing.T) {
	limiter := newLoginLimiter(2, time.Minute, time.Minute)
	key := limiterKey("127.0.0.1", "admin")

	limiter.fail(key)
	limiter.reset(key)
	limiter.fail(key)

	if !limiter.allow(key) {
		t.Fatal("a successful sign-in must clear the failures")
	}
}

func TestLoginLimiterForgetsOldFailures(t *testing.T) {
	limiter := newLoginLimiter(2, time.Minute, time.Minute)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	key := limiterKey("127.0.0.1", "admin")
	limiter.fail(key)

	// Two minutes later the window is over: the old failure does not count.
	limiter.now = func() time.Time { return now.Add(2 * time.Minute) }
	if !limiter.allow(key) {
		t.Fatal("a failure outside the window must not lock the client out")
	}
}

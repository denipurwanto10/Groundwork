package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newTestService builds a service with a fixed secret and no database, so
// the token and validation logic can be tested on its own.
func newTestService() *Service {
	return &Service{
		secret:     []byte("test-secret"),
		ttl:        time.Hour,
		refreshTTL: 24 * time.Hour,
		limiter:    newLoginLimiter(defaultLoginAttempts, defaultLoginWindow, defaultLoginCooldown),
	}
}

func TestIssueAndParseToken(t *testing.T) {
	svc := newTestService()
	user := User{ID: 42, Email: "crew@groundwork.local", Role: "operator"}

	token, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.Subject != "42" {
		t.Fatalf("subject = %q, want %q", claims.Subject, "42")
	}
	if claims.Email != user.Email {
		t.Fatalf("email = %q, want %q", claims.Email, user.Email)
	}
	if claims.Role != user.Role {
		t.Fatalf("role = %q, want %q", claims.Role, user.Role)
	}
}

func TestParseTokenRejectsGarbageAndWrongSecret(t *testing.T) {
	svc := newTestService()

	if _, err := svc.ParseToken("not-a-token"); err == nil {
		t.Fatal("expected a malformed token to be rejected")
	}

	token, err := svc.IssueToken(User{ID: 1, Email: "a@b.local", Role: "operator"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	other := &Service{secret: []byte("other-secret"), ttl: time.Hour}
	if _, err := other.ParseToken(token); err == nil {
		t.Fatal("expected a token signed with another secret to be rejected")
	}
}

func TestParseTokenRejectsExpired(t *testing.T) {
	svc := &Service{secret: []byte("test-secret"), ttl: -time.Minute}

	token, err := svc.IssueToken(User{ID: 1, Email: "a@b.local", Role: "operator"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	if _, err := svc.ParseToken(token); err == nil {
		t.Fatal("expected an expired token to be rejected")
	}
}

func TestValidateEmail(t *testing.T) {
	cases := []struct {
		email string
		valid bool
	}{
		{"crew@groundwork.local", true},
		{"", false},
		{"not-an-email", false},
		{"missing@@tld", false},
		{"@nolocal.local", false},
	}
	for _, tc := range cases {
		err := validateEmail(tc.email)
		if tc.valid && err != nil {
			t.Fatalf("email %q should be valid, got %v", tc.email, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("email %q should be rejected", tc.email)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := normalizeEmail("  Crew@Groundwork.Local "); got != "crew@groundwork.local" {
		t.Fatalf("normalized email = %q", got)
	}
}

func TestPasswordPolicyBounds(t *testing.T) {
	if MinPasswordLength != 8 || MaxPasswordLength != 72 {
		t.Fatalf("unexpected password bounds %d-%d", MinPasswordLength, MaxPasswordLength)
	}
	if !strings.Contains(ErrWeakPassword.Error(), "8-72") {
		t.Fatalf("password error should mention the bounds: %q", ErrWeakPassword)
	}
}

// A handler mounted behind RequireRole() must pass the allowed role through and
// answer every other role with the project's 403 envelope.
func TestRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := &Middleware{}

	cases := []struct {
		role string
		want int
	}{
		{RoleAdmin, http.StatusOK},
		{RoleViewer, http.StatusForbidden},
		{RoleDriver, http.StatusForbidden},
		{"", http.StatusForbidden},
	}

	for _, tc := range cases {
		r := gin.New()
		r.POST("/write",
			func(c *gin.Context) { c.Set("auth_user_role", tc.role) },
			mw.RequireRole(RoleAdmin),
			func(c *gin.Context) { c.Status(http.StatusOK) },
		)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/write", nil))

		if w.Code != tc.want {
			t.Fatalf("role %q: status = %d, want %d", tc.role, w.Code, tc.want)
		}
		if tc.want == http.StatusForbidden {
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("role %q: decode body: %v", tc.role, err)
			}
			if body.Error.Code != "forbidden" || body.Error.Message == "" {
				t.Fatalf("role %q: unexpected error envelope %s", tc.role, w.Body.String())
			}
		}
	}
}

package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Middleware rejects requests that do not carry a valid bearer token.
type Middleware struct {
	service *Service
}

// NewMiddleware creates the auth middleware.
func NewMiddleware(service *Service) *Middleware {
	return &Middleware{service: service}
}

// bearerToken extracts the token from the Authorization header. For WebSocket
// handshakes (which cannot set custom headers) it falls back to the first
// Sec-WebSocket-Protocol value, which the browser sends as a subprotocol.
func bearerToken(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1]), true
		}
		return "", false
	}
	// WebSocket handshake: browser cannot set Authorization header.
	// We accept the token as the first requested subprotocol.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		proto := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Protocol"))
		if proto != "" {
			// May contain comma-separated protocols; take the first.
			first := strings.Split(proto, ",")[0]
			return strings.TrimSpace(first), true
		}
	}
	return "", false
}

// Require returns a Gin handler that validates the Authorization header
// (or Sec-WebSocket-Protocol for WebSocket upgrades).
// On success the user id, email and role are stored in the request context.
func (m *Middleware) Require() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.Request)
		if !ok {
			abort(c, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
			return
		}

		claims, err := m.service.ParseToken(token)
		if err != nil {
			abort(c, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		c.Set("auth_user_id", claims.Subject)
		c.Set("auth_user_email", claims.Email)
		c.Set("auth_user_role", claims.Role)
		c.Next()
	}
}

// RequireRole lets the request through only when the authenticated user holds
// one of allowedRoles. It must be mounted after Require(), which is what puts
// the role in the context. The role comes from the JWT, so a role change takes
// effect on the user's next login.
func (m *Middleware) RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := UserRole(c)
		for _, allowed := range allowedRoles {
			if role == allowed {
				c.Next()
				return
			}
		}
		abort(c, http.StatusForbidden, "forbidden",
			"requires "+strings.Join(allowedRoles, " or ")+" role")
	}
}

// UserID returns the authenticated user id stored by the middleware.
func UserID(c *gin.Context) string {
	v, _ := c.Get("auth_user_id")
	id, _ := v.(string)
	return id
}

// UserEmail returns the authenticated email stored by the middleware.
func UserEmail(c *gin.Context) string {
	v, _ := c.Get("auth_user_email")
	email, _ := v.(string)
	return email
}

// UserRole returns the authenticated role stored by the middleware.
func UserRole(c *gin.Context) string {
	v, _ := c.Get("auth_user_role")
	role, _ := v.(string)
	return role
}

func abort(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

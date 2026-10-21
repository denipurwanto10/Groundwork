package handler

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"entity-management/internal/auth"

	"github.com/gin-gonic/gin"
)

// AuthHandler exposes account login, session renewal and the first-run setup.
type AuthHandler struct {
	auth *auth.Service
}

func NewAuthHandler(service *auth.Service) *AuthHandler {
	return &AuthHandler{auth: service}
}

type loginRequest struct {
	// Email is the identifier: a username or an email address.
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r loginRequest) identifier() string {
	if r.Username != "" {
		return r.Username
	}
	return r.Email
}

// Login handles POST /api/auth/login.
//
// The answer carries the access token every API call uses and the refresh
// token that renews it, so a session does not end when the access token
// expires.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be JSON with email and password"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	session, err := h.auth.Login(ctx, req.identifier(), req.Password, clientIP(c))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			c.JSON(http.StatusUnauthorized, errorBody("invalid_credentials", "invalid username or password"))
		case errors.Is(err, auth.ErrTooManyAttempts):
			c.JSON(http.StatusTooManyRequests, errorBody("too_many_attempts",
				"too many failed sign-in attempts; try again in a few minutes"))
		default:
			log.Printf("auth: login failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"token":         session.AccessToken,
		"refresh_token": session.RefreshToken,
		"expires_in":    int(session.ExpiresIn.Seconds()),
		"user":          session.User,
	}})
}

// refreshRequest is the body of POST /api/auth/refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh handles POST /api/auth/refresh: it exchanges a refresh token for a
// new access token. The account is read again, so a role change takes effect
// at the next renewal.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be JSON with refresh_token"))
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "refresh_token is required"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	session, err := h.auth.Refresh(ctx, req.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			c.JSON(http.StatusUnauthorized, errorBody("unauthorized", "the account of this session no longer exists"))
		default:
			c.JSON(http.StatusUnauthorized, errorBody("unauthorized", "invalid or expired refresh token"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"token":         session.AccessToken,
		"refresh_token": session.RefreshToken,
		"expires_in":    int(session.ExpiresIn.Seconds()),
		"user":          session.User,
	}})
}

// Status handles GET /api/auth/status. It tells the login screen whether the
// workspace still needs its first account, and how many accounts it has.
func (h *AuthHandler) Status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	count, err := h.auth.CountUsers(ctx)
	if err != nil {
		log.Printf("auth: status failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"users":          count,
		"setup_required": count == 0,
	}})
}

// setupRequest is the body of POST /api/auth/setup.
type setupRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Setup handles POST /api/auth/setup: it creates the first admin of a fresh
// workspace.
//
// It only works while the workspace has no account at all. Once one exists,
// the endpoint refuses: from then on accounts are made by an admin from the
// Users tab.
func (h *AuthHandler) Setup(c *gin.Context) {
	var req setupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be JSON with username and password"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	user, err := h.auth.CreateFirstAdmin(ctx, req.Username, req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountsExist):
			c.JSON(http.StatusConflict, errorBody("setup_closed",
				"this workspace already has an account; sign in instead"))
		case errors.Is(err, auth.ErrWeakPassword):
			c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{
				"password": "password must be 8-72 characters",
			}))
		case errors.Is(err, auth.ErrDuplicateUsername), errors.Is(err, auth.ErrDuplicateEmail):
			c.JSON(http.StatusConflict, errorBody("username_taken", "that username or email is already in use"))
		default:
			var invalid auth.ValidationError
			if errors.As(err, &invalid) {
				c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"username": invalid.Message}))
				return
			}
			log.Printf("auth: setup failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		}
		return
	}

	session, err := h.auth.NewSession(user)
	if err != nil {
		log.Printf("auth: setup session failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": gin.H{
		"token":         session.AccessToken,
		"refresh_token": session.RefreshToken,
		"expires_in":    int(session.ExpiresIn.Seconds()),
		"user":          session.User,
	}})
}

// clientIP returns the address of the caller. It keys the sign-in limiter,
// so a wrong password cannot be guessed over and over from one place.
func clientIP(c *gin.Context) string {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}

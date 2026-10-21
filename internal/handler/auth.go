package handler

import (
	"context"
	"errors"
	"log"
	"net/http"

	"entity-management/internal/auth"

	"github.com/gin-gonic/gin"
)

// AuthHandler exposes account login.
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
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be JSON with email and password"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	user, token, err := h.auth.Login(ctx, req.identifier(), req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			c.JSON(http.StatusUnauthorized, errorBody("invalid_credentials", "invalid username or password"))
			return
		}
		log.Printf("auth: login failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"token": token,
		"user":  user,
	}})
}

package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"entity-management/internal/auth"

	"github.com/gin-gonic/gin"
)

// UserHandler serves the account list and the account CRUD. Every route is
// admin-only: the routes are wired that way in cmd/server.
type UserHandler struct {
	auth *auth.Service
}

func NewUserHandler(service *auth.Service) *UserHandler {
	return &UserHandler{auth: service}
}

type createUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Gender   string `json:"gender"`
	Role     string `json:"role"`
}

type updateUserRequest struct {
	Username *string `json:"username"`
	Email    *string `json:"email"`
	Password *string `json:"password"`
	Name     *string `json:"name"`
	Gender   *string `json:"gender"`
	Role     *string `json:"role"`
}

// List handles GET /api/users. The response keeps the array shape the fleet
// form already reads for its driver picker.
func (h *UserHandler) List(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	users, err := h.auth.ListUsers(ctx)
	if err != nil {
		log.Printf("users: list failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": users})
}

// Create handles POST /api/users: an admin registers an account and picks its
// user type. Self-registration stays open but only ever creates a viewer.
func (h *UserHandler) Create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be JSON"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	user, err := h.auth.CreateUser(ctx, auth.NewUser{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
		Name:     req.Name,
		Gender:   req.Gender,
		Role:     req.Role,
	})
	if err != nil {
		writeUserError(c, "create", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": user})
}

// Update handles PUT /api/users/:id with a partial body.
func (h *UserHandler) Update(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "id must be a number"))
		return
	}

	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be JSON"))
		return
	}

	// An admin may edit their own profile, but not drop their own rights:
	// that is how a workspace loses its last admin.
	if actor, ok := actorID(c); ok && actor == id && req.Role != nil {
		c.JSON(http.StatusBadRequest, errorBody("self_role_change", "you cannot change your own user type"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	user, err := h.auth.UpdateUser(ctx, id, auth.UpdateUser{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
		Name:     req.Name,
		Gender:   req.Gender,
		Role:     req.Role,
	})
	if err != nil {
		writeUserError(c, "update", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": user})
}

// Delete handles DELETE /api/users/:id.
func (h *UserHandler) Delete(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "id must be a number"))
		return
	}

	if actor, ok := actorID(c); ok && actor == id {
		c.JSON(http.StatusBadRequest, errorBody("self_delete", "you cannot delete your own account"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	if err := h.auth.DeleteUser(ctx, id); err != nil {
		writeUserError(c, "delete", err)
		return
	}

	c.Status(http.StatusNoContent)
}

// writeUserError maps the account errors to status codes.
func writeUserError(c *gin.Context, op string, err error) {
	var invalid auth.ValidationError

	switch {
	case errors.As(err, &invalid):
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", invalid.Message))
	case errors.Is(err, auth.ErrDuplicateUsername):
		c.JSON(http.StatusConflict, errorBody("username_taken", "username already taken"))
	case errors.Is(err, auth.ErrDuplicateEmail):
		c.JSON(http.StatusConflict, errorBody("email_taken", "email already registered"))
	case errors.Is(err, auth.ErrInvalidEmail):
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", "invalid email format"))
	case errors.Is(err, auth.ErrWeakPassword):
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", "password must be 8-72 characters"))
	case errors.Is(err, auth.ErrInvalidRole):
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", "user type must be admin, viewer or driver"))
	case errors.Is(err, auth.ErrInvalidGender):
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", "gender must be male or female"))
	case errors.Is(err, auth.ErrLastAdmin):
		c.JSON(http.StatusBadRequest, errorBody("last_admin", "cannot remove the last admin"))
	case errors.Is(err, auth.ErrUserNotFound):
		c.JSON(http.StatusNotFound, errorBody("not_found", "user not found"))
	default:
		log.Printf("users: %s failed: %v", op, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
	}
}

// actorID reads the authenticated account id from the request context.
func actorID(c *gin.Context) (int64, bool) {
	raw := auth.UserID(c)
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

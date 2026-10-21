// Package auth handles user accounts, password hashing, and JWT tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"entity-management/internal/db"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	// MinPasswordLength is the shortest password accepted at registration.
	MinPasswordLength = 8
	// MaxPasswordLength is the bcrypt practical limit (72 bytes).
	MaxPasswordLength = 72
)

// Roles understood by the users_role_check constraint. Only admin may write;
// viewer and driver are read-only until the vehicle features land.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
	RoleDriver = "driver"
)

// defaultRole is used when an account is created without an explicit role:
// read-only access.
const defaultRole = RoleViewer

// ErrInvalidCredentials is returned for a wrong email or password. The same
// error is used for both so the endpoint cannot be used to enumerate accounts.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrEmailTaken is returned when the email already has an account.
var ErrEmailTaken = errors.New("email already registered")

// ErrWeakPassword is returned when the password doesn't meet policy.
var ErrWeakPassword = errors.New("password must be 8-72 characters")

// ErrInvalidEmail is returned for malformed email addresses.
var ErrInvalidEmail = errors.New("invalid email format")

// User represents an account without its password hash.
type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Gender    string    `json:"gender"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Claims are the JWT payload.
type Claims struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// Service owns the users table and the signing key.
type Service struct {
	db     *db.Pool
	secret []byte
	ttl    time.Duration
}

// NewService creates the auth service. The signing secret is required: config
// validation rejects a missing JWT_SECRET, so an empty secret is a programming
// error rather than a runtime surprise.
func NewService(pool *db.Pool, secret string, ttl time.Duration) *Service {
	if secret == "" {
		panic("auth: JWT secret must not be empty")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Service{db: pool, secret: []byte(secret), ttl: ttl}
}

// Login verifies credentials and returns the user plus a signed JWT. The
// identifier may be the username or the email address.
func (s *Service) Login(ctx context.Context, identifier, password string) (User, string, error) {
	ident := strings.ToLower(strings.TrimSpace(identifier))
	if ident == "" {
		return User{}, "", ErrInvalidCredentials
	}

	// An address matches the email column first; anything else matches the
	// username first. Both are checked so an account created with either
	// identifier can still sign in.
	preferred := "username"
	if strings.Contains(ident, "@") {
		preferred = "email"
	}

	var u User
	var hash string
	err := s.db.QueryRow(ctx, `
		SELECT id, username, COALESCE(email, ''), COALESCE(name, ''), COALESCE(gender, ''), role, created_at, password_hash
		FROM users
		WHERE lower(username) = $1 OR lower(email) = $1
		ORDER BY CASE
			WHEN $2 = 'email' AND lower(email) = $1 THEN 0
			WHEN $2 = 'username' AND lower(username) = $1 THEN 0
			ELSE 1
		END, id
		LIMIT 1
	`, ident, preferred).Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Gender, &u.Role, &u.CreatedAt, &hash)

	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return User{}, "", fmt.Errorf("query user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, "", ErrInvalidCredentials
	}

	token, err := s.IssueToken(u)
	if err != nil {
		return User{}, "", err
	}
	return u, token, nil
}

// IssueToken signs a JWT for the given user.
func (s *Service) IssueToken(u User) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: u.Username,
		Email:    u.Email,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(u.ID),
			Issuer:    "groundwork",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// ParseToken validates and parses a JWT, returning the claims.
func (s *Service) ParseToken(token string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil {
		return Claims{}, fmt.Errorf("parse token: %w", err)
	}
	return claims, nil
}

func normalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

func validateEmail(email string) error {
	_, err := mail.ParseAddress(email)
	if err != nil {
		return ErrInvalidEmail
	}
	return nil
}

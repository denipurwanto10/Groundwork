// Package auth handles user accounts, password hashing, and JWT tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
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

// ErrTooManyAttempts is returned when the sign-in limiter locked the client
// out after too many wrong passwords.
var ErrTooManyAttempts = errors.New("too many failed sign-in attempts")

// ErrAccountsExist is returned when the first-run setup is asked for but the
// workspace already has an account.
var ErrAccountsExist = errors.New("the workspace already has an account")

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
	// TokenType separates the short-lived access token from the refresh token
	// that renews it. A token without the claim is an access token: the ones
	// issued before the refresh flow existed still work.
	TokenType string `json:"typ,omitempty"`
	jwt.RegisteredClaims
}

// Token types carried in the "typ" claim.
const (
	// TokenTypeAccess is the token every API call carries.
	TokenTypeAccess = "access"
	// TokenTypeRefresh may only be exchanged for a new access token.
	TokenTypeRefresh = "refresh"
)

// Service owns the users table and the signing key.
type Service struct {
	db         *db.Pool
	secret     []byte
	ttl        time.Duration
	refreshTTL time.Duration
	limiter    *loginLimiter
}

// NewService creates the auth service. The signing secret is required: config
// validation rejects a missing JWT_SECRET, so an empty secret is a programming
// error rather than a runtime surprise.
func NewService(pool *db.Pool, secret string, ttl time.Duration) *Service {
	return NewServiceWithRefresh(pool, secret, ttl, 0)
}

// NewServiceWithRefresh creates the auth service with a separate lifetime for
// the refresh token. A refresh token that lives as long as the access token
// would not buy anything, so it gets its own (longer) window.
func NewServiceWithRefresh(pool *db.Pool, secret string, ttl, refreshTTL time.Duration) *Service {
	if secret == "" {
		panic("auth: JWT secret must not be empty")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	return &Service{
		db:         pool,
		secret:     []byte(secret),
		ttl:        ttl,
		refreshTTL: refreshTTL,
		limiter:    newLoginLimiter(defaultLoginAttempts, defaultLoginWindow, defaultLoginCooldown),
	}
}

// Login verifies credentials and returns the user plus a signed JWT. The
// identifier may be the username or the email address.
func (s *Service) Login(ctx context.Context, identifier, password, client string) (Session, error) {
	ident := strings.ToLower(strings.TrimSpace(identifier))
	if ident == "" {
		return Session{}, ErrInvalidCredentials
	}

	key := limiterKey(client, ident)
	if !s.limiter.allow(key) {
		return Session{}, ErrTooManyAttempts
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
		s.limiter.fail(key)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("query user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		s.limiter.fail(key)
		return Session{}, ErrInvalidCredentials
	}

	s.limiter.reset(key)
	return s.NewSession(u)
}

// limiterKey builds the key of the sign-in limiter: the client address and
// the account that was tried, so one noisy client cannot lock out everybody
// and one account cannot be attacked from many places at full speed.
func limiterKey(client, identifier string) string {
	return strings.TrimSpace(client) + "|" + strings.ToLower(identifier)
}

// CountUsers returns how many accounts the workspace has. A fresh database
// has none, and the login screen then offers to create the first admin.
func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateFirstAdmin creates the very first account of a fresh workspace.
//
// It is the way into a database that was just migrated: there is no account
// to sign in with, and an admin cannot create one either. The call is refused
// as soon as one account exists, so it cannot be used to take over a running
// workspace.
func (s *Service) CreateFirstAdmin(ctx context.Context, username, email, password string) (User, error) {
	count, err := s.CountUsers(ctx)
	if err != nil {
		return User{}, err
	}
	if count > 0 {
		return User{}, ErrAccountsExist
	}

	return s.CreateUser(ctx, NewUser{
		Username: username,
		Email:    email,
		Password: password,
		Name:     username,
		Role:     RoleAdmin,
	})
}

// IssueToken signs a JWT for the given user.
func (s *Service) IssueToken(u User) (string, error) {
	return s.sign(u, TokenTypeAccess, s.ttl)
}

// IssueRefreshToken signs the token that renews an access token. It carries
// the same claims, but a different type and a longer lifetime, so it cannot
// be used as a bearer token for the API.
func (s *Service) IssueRefreshToken(u User) (string, error) {
	return s.sign(u, TokenTypeRefresh, s.refreshTTL)
}

// sign builds a token of the given type and lifetime.
func (s *Service) sign(u User, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Username:  u.Username,
		Email:     u.Email,
		Role:      u.Role,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(u.ID),
			Issuer:    "groundwork",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// ParseToken validates and parses an access token, returning the claims.
// A refresh token is refused: it may only be exchanged, not used.
func (s *Service) ParseToken(token string) (Claims, error) {
	claims, err := s.parse(token)
	if err != nil {
		return Claims{}, err
	}
	if claims.TokenType == TokenTypeRefresh {
		return Claims{}, fmt.Errorf("parse token: refresh token cannot be used as an access token")
	}
	return claims, nil
}

// ParseRefreshToken validates and parses a refresh token.
func (s *Service) ParseRefreshToken(token string) (Claims, error) {
	claims, err := s.parse(token)
	if err != nil {
		return Claims{}, err
	}
	if claims.TokenType != TokenTypeRefresh {
		return Claims{}, fmt.Errorf("parse token: not a refresh token")
	}
	return claims, nil
}

// parse validates the signature and the expiry of any token.
func (s *Service) parse(token string) (Claims, error) {
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

// Session is what a successful sign-in hands out: the access token the API
// calls carry, and the refresh token that renews it.
type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
	User         User
}

// NewSession signs a fresh pair of tokens for a user.
func (s *Service) NewSession(u User) (Session, error) {
	access, err := s.sign(u, TokenTypeAccess, s.ttl)
	if err != nil {
		return Session{}, err
	}
	refresh, err := s.sign(u, TokenTypeRefresh, s.refreshTTL)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: refresh, ExpiresIn: s.ttl, User: u}, nil
}

// Refresh exchanges a refresh token for a new session. The account is read
// again, so a role change or a deleted account takes effect at the next
// renewal instead of at the end of the access token's life.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	claims, err := s.ParseRefreshToken(refreshToken)
	if err != nil {
		return Session{}, err
	}

	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Session{}, fmt.Errorf("parse token: bad subject")
	}

	u, err := s.UserByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	return s.NewSession(u)
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

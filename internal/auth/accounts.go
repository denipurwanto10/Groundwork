package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// Errors the account handlers translate into HTTP status codes.
var (
	// ErrDuplicateUsername is returned when the username is already in use.
	ErrDuplicateUsername = errors.New("username already taken")
	// ErrDuplicateEmail is returned when the email address is already in use.
	ErrDuplicateEmail = errors.New("email already registered")
	// ErrInvalidRole is returned for a user type outside the known set.
	ErrInvalidRole = errors.New("invalid user type")
	// ErrInvalidGender is returned for a gender outside the known set.
	ErrInvalidGender = errors.New("invalid gender")
	// ErrUserNotFound is returned when the account does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrLastAdmin is returned when the change would leave no admin behind.
	ErrLastAdmin = errors.New("cannot remove the last admin")
)

// usernamePattern keeps usernames to characters that survive a URL, a form
// field and a shell without any escaping.
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)

// NewUser is an account created by an admin.
type NewUser struct {
	Username string
	Email    string
	Password string
	Name     string
	Gender   string
	Role     string
}

// UpdateUser is a partial update: a nil field stays as it is. Password, when
// set, replaces the stored hash.
type UpdateUser struct {
	Username *string
	Email    *string
	Password *string
	Name     *string
	Gender   *string
	Role     *string
}

// ValidationError marks an input the service refused because of its format.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// ValidateRole checks a user type against the CHECK constraint on the column.
func ValidateRole(role string) error {
	switch role {
	case RoleAdmin, RoleViewer, RoleDriver:
		return nil
	}
	return ErrInvalidRole
}

// ValidateGender checks a gender value against the CHECK constraint.
func ValidateGender(gender string) error {
	switch gender {
	case "", "male", "female":
		return nil
	}
	return ErrInvalidGender
}

// ValidateUsername checks the username format.
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return ValidationError{Message: "username must be 3-64 characters (letters, digits, dot, dash, underscore)"}
	}
	return nil
}

// CreateUser stores a new account with the given user type.
func (s *Service) CreateUser(ctx context.Context, in NewUser) (User, error) {
	username := strings.TrimSpace(in.Username)
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}

	email := ""
	if strings.TrimSpace(in.Email) != "" {
		email = normalizeEmail(in.Email)
		if err := validateEmail(email); err != nil {
			return User{}, err
		}
	}

	if len(in.Password) < MinPasswordLength || len(in.Password) > MaxPasswordLength {
		return User{}, ErrWeakPassword
	}

	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = defaultRole
	}
	if err := ValidateRole(role); err != nil {
		return User{}, err
	}

	gender := strings.ToLower(strings.TrimSpace(in.Gender))
	if err := ValidateGender(gender); err != nil {
		return User{}, err
	}

	name := strings.TrimSpace(in.Name)
	if len(name) > 120 {
		name = name[:120]
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}

	var u User
	err = s.db.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, name, gender, role)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, username, COALESCE(email, ''), COALESCE(name, ''), COALESCE(gender, ''), role, created_at
	`, username, nullIfEmpty(email), string(hash), nullIfEmpty(name), nullIfEmpty(gender), role).
		Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Gender, &u.Role, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "idx_users_username_lower" {
				return User{}, ErrDuplicateUsername
			}
			return User{}, ErrDuplicateEmail
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// UpdateUser changes the given fields of one account.
func (s *Service) UpdateUser(ctx context.Context, id int64, in UpdateUser) (User, error) {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 7)
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if in.Username != nil {
		username := strings.TrimSpace(*in.Username)
		if err := ValidateUsername(username); err != nil {
			return User{}, err
		}
		add("username", username)
	}

	if in.Email != nil {
		email := ""
		if strings.TrimSpace(*in.Email) != "" {
			email = normalizeEmail(*in.Email)
			if err := validateEmail(email); err != nil {
				return User{}, err
			}
		}
		add("email", nullIfEmpty(email))
	}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if len(name) > 120 {
			name = name[:120]
		}
		add("name", nullIfEmpty(name))
	}

	if in.Gender != nil {
		gender := strings.ToLower(strings.TrimSpace(*in.Gender))
		if err := ValidateGender(gender); err != nil {
			return User{}, err
		}
		add("gender", nullIfEmpty(gender))
	}

	if in.Role != nil {
		role := strings.TrimSpace(*in.Role)
		if err := ValidateRole(role); err != nil {
			return User{}, err
		}
		add("role", role)
	}

	if in.Password != nil && *in.Password != "" {
		if len(*in.Password) < MinPasswordLength || len(*in.Password) > MaxPasswordLength {
			return User{}, ErrWeakPassword
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if err != nil {
			return User{}, fmt.Errorf("hash password: %w", err)
		}
		add("password_hash", string(hash))
	}

	if len(sets) == 0 {
		return s.UserByID(ctx, id)
	}

	// Demoting an admin may not leave the workspace without one.
	if in.Role != nil && *in.Role != RoleAdmin {
		wasAdmin, err := s.isAdmin(ctx, id)
		if err != nil {
			return User{}, err
		}
		if wasAdmin {
			admins, err := s.countAdmins(ctx)
			if err != nil {
				return User{}, err
			}
			if admins <= 1 {
				return User{}, ErrLastAdmin
			}
		}
	}

	args = append(args, id)
	query := fmt.Sprintf(`
		UPDATE users SET %s
		WHERE id = $%d
		RETURNING id, username, COALESCE(email, ''), COALESCE(name, ''), COALESCE(gender, ''), role, created_at
	`, strings.Join(sets, ", "), len(args))

	var u User
	err := s.db.QueryRow(ctx, query, args...).
		Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Gender, &u.Role, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "idx_users_username_lower" {
				return User{}, ErrDuplicateUsername
			}
			return User{}, ErrDuplicateEmail
		}
		return User{}, fmt.Errorf("update user: %w", err)
	}
	return u, nil
}

// DeleteUser removes an account. Vehicles keep their rows: the driver link is
// cleared by the foreign key.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	wasAdmin, err := s.isAdmin(ctx, id)
	if err != nil {
		return err
	}
	if wasAdmin {
		admins, err := s.countAdmins(ctx)
		if err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}

	tag, err := s.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UserByID reads one account without its password hash.
func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT id, username, COALESCE(email, ''), COALESCE(name, ''), COALESCE(gender, ''), role, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Gender, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}

// ListUsers returns every account, without password hashes.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, username, COALESCE(email, ''), COALESCE(name, ''), COALESCE(gender, ''), role, created_at
		FROM users ORDER BY username ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Gender, &u.Role, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows users: %w", err)
	}
	return users, nil
}

// isAdmin reports whether the account still has the admin user type.
func (s *Service) isAdmin(ctx context.Context, id int64) (bool, error) {
	var role string
	err := s.db.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, fmt.Errorf("query role: %w", err)
	}
	return role == RoleAdmin, nil
}

// countAdmins counts the accounts that may still manage the workspace.
func (s *Service) countAdmins(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = $1`, RoleAdmin).Scan(&n); err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}

// nullIfEmpty maps an empty string to a SQL NULL so the column keeps its
// "not set" state instead of storing a blank.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

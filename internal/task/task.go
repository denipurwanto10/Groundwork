// Package task holds the work-order model: the job a unit is sent out for.
//
// A vehicle was driving without a task. The task is what makes the trip mean
// something: what has to be done, who does it, where it goes and by when.
//
// The validation here mirrors the CHECK constraints in
// migrations/021_create_tasks.up.sql, so a value the API rejects can never
// reach the database either.
package task

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// The three steps a work order walks through.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
)

// Statuses lists the statuses in the order the board shows them.
var Statuses = []string{StatusPending, StatusInProgress, StatusCompleted}

var statuses = map[string]bool{
	StatusPending:    true,
	StatusInProgress: true,
	StatusCompleted:  true,
}

// The lengths the VARCHAR columns of the table allow.
const (
	MaxTitleLen       = 160
	MaxDescriptionLen = 2000
)

// DateLayout is the calendar day of a deadline: "2026-10-25".
const DateLayout = "2006-01-02"

// FieldErrors maps a field name to the reason it was rejected. The handler
// turns it into the same validation envelope the entity and vehicle endpoints
// use.
type FieldErrors map[string]string

// Error implements the error interface so a FieldErrors can be returned as one.
func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+f[k])
	}
	return strings.Join(parts, "; ")
}

// Date is a calendar day without a time zone: the deadline of a work order is
// a day, not a moment. It travels over the wire as "2026-10-25".
type Date struct {
	time.Time
}

// NewDate builds a Date from a calendar day.
func NewDate(t time.Time) Date {
	return Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)}
}

// ParseDate reads a calendar day written as "2026-10-25".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return NewDate(t), nil
}

// MarshalJSON writes the day as "2026-10-25".
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.Time.Format(DateLayout) + `"`), nil
}

// Task is one work order.
type Task struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	// The account that has to do the work. Null means nobody is assigned.
	AssignedUserID *int64 `json:"assigned_user_id"`
	// The place the work is about. Null means no destination was picked.
	PlaceID *int64 `json:"place_id"`
	// The day the work has to be done by.
	DueDate   *Date     `json:"due_date"`
	CreatedAt time.Time `json:"created_at"`
	// The name of the assigned account, so the card can be drawn without a
	// second request. Empty when the account is gone.
	AssignedName string `json:"assigned_name"`
	// The name of the place, for the same reason.
	PlaceName string `json:"place_name"`
}

// Input is the body of POST /api/tasks and PUT /api/tasks/:id.
type Input struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	AssignedUserID *int64 `json:"assigned_user_id"`
	PlaceID        *int64 `json:"place_id"`
	// DueDate is the calendar day the work has to be done by, written as
	// "2026-10-25". Empty means no deadline.
	DueDate string `json:"due_date"`
}

// Normalize trims the text fields and applies the default status.
// Run before Validate so the stored values are clean.
func (in *Input) Normalize() {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	if in.Status == "" {
		in.Status = StatusPending
	}
	in.DueDate = strings.TrimSpace(in.DueDate)
}

// Validate reports every rejected field at once. An empty map means the
// input is valid. The bounds mirror the VARCHAR sizes and the CHECK
// constraints of the table.
//
// The title and the description are counted in characters, not bytes: the
// columns are VARCHAR, so a title of 160 Indonesian letters fits as well as a
// title of 160 latin ones.
func (in *Input) Validate() FieldErrors {
	errs := FieldErrors{}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		errs["title"] = "title is required"
	} else if utf8.RuneCountInString(title) > MaxTitleLen {
		errs["title"] = fmt.Sprintf("title must be at most %d characters", MaxTitleLen)
	}

	if utf8.RuneCountInString(strings.TrimSpace(in.Description)) > MaxDescriptionLen {
		errs["description"] = fmt.Sprintf("description must be at most %d characters", MaxDescriptionLen)
	}

	if !statuses[in.Status] {
		errs["status"] = "status must be pending, in_progress or completed"
	}

	if in.AssignedUserID != nil && *in.AssignedUserID <= 0 {
		errs["assigned_user_id"] = "assigned_user_id must be a positive number"
	}

	if in.PlaceID != nil && *in.PlaceID <= 0 {
		errs["place_id"] = "place_id must be a positive number"
	}

	if in.DueDate != "" {
		if _, err := ParseDate(in.DueDate); err != nil {
			errs["due_date"] = "due_date must be a date like 2026-10-25"
		}
	}

	return errs
}

// StatusInput is the body of PUT /api/tasks/:id/status: the one field the
// driver and the staff may change.
type StatusInput struct {
	Status string `json:"status"`
}

// Normalize trims the status and applies the default.
func (in *StatusInput) Normalize() {
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	if in.Status == "" {
		in.Status = StatusPending
	}
}

// Validate reports a status outside the known set.
func (in *StatusInput) Validate() FieldErrors {
	errs := FieldErrors{}
	if !statuses[in.Status] {
		errs["status"] = "status must be pending, in_progress or completed"
	}
	return errs
}

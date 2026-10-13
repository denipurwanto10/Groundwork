// Package entity defines the Entity domain model and input validation.
//
// Validation here mirrors the database CHECK constraints in
// migrations/001_create_entities.up.sql so invalid data is rejected
// before it reaches PostgreSQL.
package entity

import (
	"strings"
	"time"
)

const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

const (
	maxNameLen = 255
	maxTypeLen = 100
)

type Entity struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EntityInput is the writable subset of Entity used by POST and PUT.
type EntityInput struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Status    string  `json:"status"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Normalize trims whitespace and applies the default status.
// Run before Validate so stored values are clean.
func (in *EntityInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.TrimSpace(in.Type)
	in.Status = strings.TrimSpace(in.Status)
	if in.Status == "" {
		in.Status = StatusActive
	}
}

// Validate reports one message per invalid field.
// An empty map means the input is valid. Bounds and length limits
// mirror the DB CHECK constraints and VARCHAR sizes.
func (in *EntityInput) Validate() map[string]string {
	errs := make(map[string]string)

	if strings.TrimSpace(in.Name) == "" {
		errs["name"] = "name is required"
	} else if len(in.Name) > maxNameLen {
		errs["name"] = "name must be at most 255 characters"
	}

	if strings.TrimSpace(in.Type) == "" {
		errs["type"] = "type is required"
	} else if len(in.Type) > maxTypeLen {
		errs["type"] = "type must be at most 100 characters"
	}

	if in.Status != StatusActive && in.Status != StatusInactive {
		errs["status"] = "status must be 'active' or 'inactive'"
	}

	if in.Latitude < -90 || in.Latitude > 90 {
		errs["latitude"] = "latitude must be between -90 and 90"
	}

	if in.Longitude < -180 || in.Longitude > 180 {
		errs["longitude"] = "longitude must be between -180 and 180"
	}

	return errs
}

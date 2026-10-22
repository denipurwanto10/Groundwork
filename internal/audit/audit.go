// Package audit records who changed what.
//
// Every successful write leaves one row behind: the account that made the
// request, what it did, to which object, and when. The recording happens in
// a middleware (see middleware.go), so no handler can forget to log it and
// a new handler is audited without being touched.
package audit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"entity-management/internal/db"
)

// Entry is one recorded action, on its way into the table.
type Entry struct {
	// UserID points at the account that made the request. It is empty when
	// the account is unknown (or already deleted).
	UserID *int64
	// UserEmail is the identity of that account, copied into the row: the
	// trail has to outlive the account it talks about.
	UserEmail string
	// Action is what happened: create, update, delete, import, set, clear.
	Action string
	// EntityType is the kind of object it happened to: zone, vehicle, …
	EntityType string
	// EntityID is the object itself, when the request named one.
	EntityID *int64
	// Details is the sentence the operator reads.
	Details string
}

// Log is one row of the trail, as the API hands it out.
type Log struct {
	ID         int64     `json:"id"`
	UserID     *int64    `json:"user_id"`
	UserEmail  string    `json:"user_email"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   *int64    `json:"entity_id"`
	Details    string    `json:"details"`
	CreatedAt  time.Time `json:"created_at"`
}

// Filter narrows the trail down. Every field is optional: a nil or empty one
// does not narrow anything.
type Filter struct {
	// Limit is how many rows are returned, newest first.
	Limit int
	// Offset skips rows, for paging through a long trail.
	Offset int
	// Action keeps only one kind of action.
	Action string
	// EntityType keeps only one kind of object.
	EntityType string
	// UserID keeps only the rows of one account.
	UserID *int64
	// Query keeps only the rows whose text holds this fragment, in the
	// sentence or in the name of the account.
	Query string
}

// Recorder writes and reads the trail.
type Recorder struct {
	db *db.Pool
}

// NewRecorder creates the recorder. The pool is the same one the handlers
// use, so the trail lives next to the data it talks about.
func NewRecorder(pool *db.Pool) *Recorder {
	return &Recorder{db: pool}
}

// Record writes one entry.
func (r *Recorder) Record(ctx context.Context, e Entry) error {
	_, err := r.db.Exec(ctx, `INSERT INTO audit_logs (user_id, user_email, action, entity_type, entity_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		e.UserID, e.UserEmail, e.Action, e.EntityType, e.EntityID, e.Details)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

// listSQL reads the trail newest first. The filters are nullable parameters
// instead of built SQL, so the query stays the same whatever is filtered on.
const listSQL = `SELECT id, user_id, COALESCE(user_email, ''), action, entity_type,
		entity_id, COALESCE(details, ''), created_at
	FROM audit_logs
	WHERE ($1::text IS NULL OR action = $1)
	  AND ($2::text IS NULL OR entity_type = $2)
	  AND ($3::bigint IS NULL OR user_id = $3)
	  AND ($4::text IS NULL OR details ILIKE '%' || $4 || '%'
	                       OR COALESCE(user_email, '') ILIKE '%' || $4 || '%')
	ORDER BY created_at DESC, id DESC
	LIMIT $5 OFFSET $6`

// countSQL counts the rows the same filter keeps, so the panel can say how
// much there is behind the page it shows.
const countSQL = `SELECT count(*)
	FROM audit_logs
	WHERE ($1::text IS NULL OR action = $1)
	  AND ($2::text IS NULL OR entity_type = $2)
	  AND ($3::bigint IS NULL OR user_id = $3)
	  AND ($4::text IS NULL OR details ILIKE '%' || $4 || '%'
	                       OR COALESCE(user_email, '') ILIKE '%' || $4 || '%')`

// List returns the newest entries of the trail and how many entries the
// filter keeps in total.
func (r *Recorder) List(ctx context.Context, f Filter) ([]Log, int, error) {
	action := nullableText(f.Action)
	entityType := nullableText(f.EntityType)
	query := nullableText(strings.TrimSpace(f.Query))

	var total int
	if err := r.db.QueryRow(ctx, countSQL, action, entityType, f.UserID, query).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	rows, err := r.db.Query(ctx, listSQL, action, entityType, f.UserID, query, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query audit logs: %w", err)
	}
	defer rows.Close()

	logs := make([]Log, 0, f.Limit)
	for rows.Next() {
		var l Log
		if err := rows.Scan(
			&l.ID, &l.UserID, &l.UserEmail, &l.Action, &l.EntityType,
			&l.EntityID, &l.Details, &l.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read audit logs: %w", err)
	}

	return logs, total, nil
}

// nullableText turns an empty filter into a NULL, so the SQL above can use
// it as "no filter" without building a different query.
func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

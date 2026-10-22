package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/db"
	"entity-management/internal/push"
	"entity-management/internal/task"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TaskHandler serves the work orders: the jobs the fleet is sent out for.
//
// Every role may read the board. Writing a task (creating, editing, removing
// it) is admin-only; moving a task along is what the field does, so a driver
// or a staff member may do that — a driver only on the tasks that are theirs.
type TaskHandler struct {
	db *db.Pool
	// push tells the assigned account that a work order is waiting for them.
	push *push.Service
}

func NewTaskHandler(pool *db.Pool, pushService *push.Service) *TaskHandler {
	return &TaskHandler{db: pool, push: pushService}
}

func (h *TaskHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), dbTimeout)
}

// taskCols reads a task together with the names the card shows: who has to
// do the work and which place it is about.
//
// The joins are LEFT joins on purpose: a task whose account or whose place
// was deleted keeps its row, it just loses the name.
const taskCols = `SELECT t.id, t.title, t.description, t.status,
       t.assigned_user_id, t.place_id, t.due_date, t.created_at,
       COALESCE(NULLIF(u.name, ''), u.username, ''),
       COALESCE(e.name, '')
  FROM tasks t
  LEFT JOIN users u    ON u.id = t.assigned_user_id
  LEFT JOIN entities e ON e.id = t.place_id`

// scanTask reads one task row.
func scanTask(row pgx.Row) (task.Task, error) {
	var t task.Task
	var due *time.Time
	err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.Status,
		&t.AssignedUserID, &t.PlaceID, &due, &t.CreatedAt,
		&t.AssignedName, &t.PlaceName,
	)
	if err != nil {
		return task.Task{}, err
	}
	if due != nil {
		d := task.NewDate(*due)
		t.DueDate = &d
	}
	return t, nil
}

// List handles GET /api/tasks.
//
// A driver only receives the tasks that are assigned to them: the board of a
// driver is their own work, not the work of the whole company. Everybody else
// sees every task.
func (h *TaskHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	query := taskCols
	var args []any

	if scope := taskScope(c); scope != nil {
		query += " WHERE t.assigned_user_id = $1"
		args = append(args, *scope)
	}

	// The deadline first: the work that has to be done today sits on top of
	// its column. A task without a deadline keeps to the bottom.
	query += " ORDER BY t.due_date ASC NULLS LAST, t.created_at DESC, t.id DESC"

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		log.Printf("tasks: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	tasks := make([]task.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			log.Printf("tasks: list scan failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("tasks: list rows failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": tasks})
}

// Get handles GET /api/tasks/:id.
func (h *TaskHandler) Get(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid task id"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	t, err := scanTask(h.db.QueryRow(ctx, taskCols+" WHERE t.id = $1", id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "task not found"))
			return
		}
		log.Printf("tasks: get id=%d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// A driver may not read the tasks of somebody else.
	if !h.maySee(c, t) {
		c.JSON(http.StatusForbidden, errorBody("forbidden", "this task is not yours"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": t})
}

// Create handles POST /api/tasks.
func (h *TaskHandler) Create(c *gin.Context) {
	var in task.Input
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	due, err := h.dueDate(in.DueDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"due_date": err.Error()}))
		return
	}

	var id int64
	err = h.db.QueryRow(ctx, `INSERT INTO tasks (title, description, status, assigned_user_id, place_id, due_date)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		in.Title, in.Description, in.Status, in.AssignedUserID, in.PlaceID, due).Scan(&id)
	if err != nil {
		writeTaskError(c, "create", err)
		return
	}

	created, err := scanTask(h.db.QueryRow(ctx, taskCols+" WHERE t.id = $1", id))
	if err != nil {
		log.Printf("tasks: read-back failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// Somebody was put on the job: that person hears about it, and nobody
	// else does.
	h.notifyTaskAssigned(ctx, created)

	c.JSON(http.StatusCreated, gin.H{"data": created})
}

// notifyTaskAssigned tells the assigned account that a work order is
// waiting for them.
//
// The receiver is the one the task was assigned to — not the whole board.
// The push goes out in the background, so it never holds this answer up.
//
// The link of the notification is the trip screen for a driver and the
// board for everybody else.
func (h *TaskHandler) notifyTaskAssigned(ctx context.Context, t task.Task) {
	if h.push == nil || t.AssignedUserID == nil {
		return
	}

	body := t.Title
	if t.DueDate != nil {
		body += " — tenggat " + t.DueDate.Format(task.DateLayout)
	}

	url := "/"
	var role string
	err := h.db.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, *t.AssignedUserID).Scan(&role)
	if err != nil {
		log.Printf("tasks: read the role of account %d failed: %v", *t.AssignedUserID, err)
	} else if role == auth.RoleDriver {
		url = "/driver"
	}

	payload := push.Payload{
		Title: "Tugas baru",
		Body:  body,
		Tag:   fmt.Sprintf("task-assigned-%d", t.ID),
		URL:   url,
	}

	assigned := *t.AssignedUserID
	push.Background(func(ctx context.Context) {
		h.push.NotifyUser(ctx, assigned, payload)
	})
}

// Update handles PUT /api/tasks/:id (full replacement).
func (h *TaskHandler) Update(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid task id"))
		return
	}

	var in task.Input
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	due, err := h.dueDate(in.DueDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"due_date": err.Error()}))
		return
	}

	// The update writes the row; the read-back with the joins fills in the
	// name of the account and of the place, which the UPDATE itself does not
	// carry.
	var updatedID int64
	err = h.db.QueryRow(ctx, `UPDATE tasks
			SET title = $2, description = $3, status = $4, assigned_user_id = $5, place_id = $6, due_date = $7
		WHERE id = $1
		RETURNING id`,
		id, in.Title, in.Description, in.Status, in.AssignedUserID, in.PlaceID, due).Scan(&updatedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "task not found"))
			return
		}
		writeTaskError(c, "update", err)
		return
	}

	updated, err := scanTask(h.db.QueryRow(ctx, taskCols+" WHERE t.id = $1", updatedID))
	if err != nil {
		log.Printf("tasks: read-back failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// Delete handles DELETE /api/tasks/:id.
func (h *TaskHandler) Delete(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid task id"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	res, err := h.db.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		log.Printf("tasks: delete id=%d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, errorBody("not_found", "task not found"))
		return
	}

	c.Status(http.StatusNoContent)
}

// UpdateStatus handles PUT /api/tasks/:id/status.
//
// This is the endpoint the field uses: the driver marks the job as done, the
// staff moves a job along. A driver may only move the tasks that are theirs;
// the staff and the admin may move any task.
func (h *TaskHandler) UpdateStatus(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid task id"))
		return
	}

	var in task.StatusInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	// A driver may only work on their own tasks: the row is locked with the
	// scope, so a task of somebody else is simply not found.
	query := `UPDATE tasks SET status = $2 WHERE id = $1`
	args := []any{id, in.Status}
	if scope := taskScope(c); scope != nil {
		query += " AND assigned_user_id = $3"
		args = append(args, *scope)
	}
	query += " RETURNING id"

	var updatedID int64
	err := h.db.QueryRow(ctx, query, args...).Scan(&updatedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either the task does not exist, or it belongs to another
			// driver. The answer is the same either way: the driver must not
			// learn which one it is.
			c.JSON(http.StatusNotFound, errorBody("not_found", "task not found"))
			return
		}
		log.Printf("tasks: status update id=%d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	updated, err := scanTask(h.db.QueryRow(ctx, taskCols+" WHERE t.id = $1", updatedID))
	if err != nil {
		log.Printf("tasks: read-back failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// taskScope returns the id of the signed-in driver, or nil for the roles that
// see the whole board. An unparsable subject yields id 0, which matches no
// task: a malformed token must not widen the view.
func taskScope(c *gin.Context) *int64 {
	if auth.UserRole(c) != auth.RoleDriver {
		return nil
	}
	id, err := strconv.ParseInt(auth.UserID(c), 10, 64)
	if err != nil {
		id = 0
	}
	return &id
}

// maySee reports whether the signed-in account may read this task. Only a
// driver is limited: to the tasks that are theirs.
func (h *TaskHandler) maySee(c *gin.Context, t task.Task) bool {
	scope := taskScope(c)
	if scope == nil {
		return true
	}
	return t.AssignedUserID != nil && *t.AssignedUserID == *scope
}

// dueDate turns the calendar day of the body into the value the column
// takes. An empty day means no deadline.
//
// The value that goes into the database is a plain time.Time: the driver
// understands that one, while the Date type is what the JSON carries.
func (h *TaskHandler) dueDate(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	d, err := task.ParseDate(raw)
	if err != nil {
		return nil, err
	}
	t := d.Time
	return &t, nil
}

// writeTaskError maps the database errors of a task write to status codes.
func writeTaskError(c *gin.Context, op string, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			// A foreign key that does not hold: the account or the place
			// behind the id is not there.
			fields := map[string]string{}
			if strings.Contains(pgErr.ConstraintName, "assigned_user_id") {
				fields["assigned_user_id"] = "unknown user"
			} else {
				fields["place_id"] = "unknown place"
			}
			c.JSON(http.StatusBadRequest, validationErrorBody(fields))
			return
		case "23502", "23514":
			c.JSON(http.StatusBadRequest, errorBody("validation_failed", "validation failed"))
			return
		}
	}
	log.Printf("tasks: %s failed: %v", op, err)
	c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
}

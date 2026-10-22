package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/auth"

	"github.com/gin-gonic/gin"
)

// writeTimeout bounds the insert of one entry. The trail is written while
// the request is on its way out, so it cannot hold the answer for long.
const writeTimeout = 3 * time.Second

// The actions the trail knows.
const (
	actionCreate = "create"
	actionUpdate = "update"
	actionDelete = "delete"
	actionImport = "import"
	actionSet    = "set"
	actionClear  = "clear"
)

// The kinds of object the trail knows.
const (
	entityEntity  = "entity"
	entityZone    = "zone"
	entityVehicle = "vehicle"
	entityUser    = "user"
	entityRoute   = "route"
	entityTask    = "task"
)

// entityLabels are the words the operator reads, not the names of the
// tables: a place is called a place everywhere in the interface.
var entityLabels = map[string]string{
	entityEntity:  "place",
	entityZone:    "zone",
	entityVehicle: "vehicle",
	entityUser:    "account",
	entityRoute:   "route",
	entityTask:    "task",
}

// subjectQueries read the name of a row that is about to disappear: a delete
// removes it, so its name has to be looked up before the delete happens.
var subjectQueries = map[string]string{
	entityEntity:  `SELECT name FROM entities WHERE id = $1`,
	entityZone:    `SELECT name FROM zones WHERE id = $1`,
	entityVehicle: `SELECT name FROM vehicles WHERE id = $1`,
	entityUser:    `SELECT username FROM users WHERE id = $1`,
	entityTask:    `SELECT title FROM tasks WHERE id = $1`,
}

// Middleware records every successful write.
//
// It has to run after the auth middleware: that is what puts the account in
// the request context. A request that failed changed nothing, so it is not
// recorded — the trail holds what happened, not what was tried.
func (r *Recorder) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		action, entityType, entityID, ok := describe(c)
		if !ok {
			c.Next()
			return
		}

		// The row a delete removes is gone once the handler returns, and a
		// route is named after its vehicle, so both are read up front.
		name := ""
		if action == actionDelete || entityType == entityRoute {
			name = r.subjectName(c.Request.Context(), entityType, entityID)
		}

		// The answer of the handler carries the name of the object it created
		// or changed, so it is kept on the way out.
		captured := &capture{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = captured

		c.Next()

		c.Writer = captured.ResponseWriter

		if c.Writer.Status() >= http.StatusBadRequest {
			return
		}

		body := captured.body.Bytes()
		if name == "" {
			name = subjectFromResponse(body, entityType)
		}
		if entityID == nil {
			if id, ok := idFromResponse(body); ok {
				entityID = &id
			}
		}

		entry := Entry{
			UserID:     actorID(c),
			UserEmail:  actorEmail(c),
			Action:     action,
			EntityType: entityType,
			EntityID:   entityID,
			Details:    details(action, entityType, name, entityID, importedFromResponse(body)),
		}

		// The request context is cancelled as soon as the answer is on its
		// way, so the trail is written with a context of its own.
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.Record(ctx, entry); err != nil {
			// A missing entry must never fail the request that already
			// succeeded: the change is done, only the note about it is lost.
			log.Printf("audit: record %s %s failed: %v", action, entityType, err)
		}
	}
}

// capture keeps a copy of the answer while it is written to the client.
type capture struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (c *capture) Write(data []byte) (int, error) {
	c.body.Write(data)
	return c.ResponseWriter.Write(data)
}

func (c *capture) WriteString(s string) (int, error) {
	c.body.WriteString(s)
	return c.ResponseWriter.WriteString(s)
}

// describe turns the request into the action and the kind of object it
// touched. The URL is the source of truth, so a new endpoint is audited
// without anybody having to remember it.
func describe(c *gin.Context) (action, entityType string, entityID *int64, ok bool) {
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		// Reads are not changes.
		return "", "", nil, false
	}

	segments := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
	if len(segments) < 2 || segments[0] != "api" {
		return "", "", nil, false
	}

	collection := segments[1]
	entityType = singular(collection)
	last := segments[len(segments)-1]

	switch last {
	case "position":
		// A position report is telemetry, not a change of a record. It
		// arrives every couple of seconds and would bury the trail.
		return "", "", nil, false
	case "route":
		entityType = entityRoute
		if c.Request.Method == http.MethodDelete {
			action = actionClear
		} else {
			action = actionSet
		}
	case "import":
		action = actionImport
	default:
		switch c.Request.Method {
		case http.MethodPost:
			// A zone made from a region or an island is still a new zone.
			action = actionCreate
		case http.MethodPut, http.MethodPatch:
			action = actionUpdate
		case http.MethodDelete:
			action = actionDelete
		}
	}

	// The id of the object is the first number behind the collection.
	for _, segment := range segments[2:] {
		if id, err := strconv.ParseInt(segment, 10, 64); err == nil && id > 0 {
			entityID = &id
			break
		}
	}

	return action, entityType, entityID, true
}

// singular turns the name of a collection into the kind of object it holds.
func singular(collection string) string {
	switch collection {
	case "entities":
		return entityEntity
	case "zones":
		return entityZone
	case "vehicles":
		return entityVehicle
	case "users":
		return entityUser
	case "routes":
		return entityRoute
	case "tasks":
		return entityTask
	default:
		// An unknown collection is still worth a row: the action and the
		// collection name say enough.
		return collection
	}
}

// details turns the recorded action into the sentence the operator reads:
// "created zone \"Gudang Utama\"".
func details(action, entityType, name string, id *int64, imported int) string {
	label := entityLabels[entityType]
	if label == "" {
		label = entityType
	}

	switch action {
	case actionImport:
		return fmt.Sprintf("imported %d zone(s)", imported)
	case actionSet:
		return withSubject("planned a route for vehicle", name, id)
	case actionClear:
		return withSubject("cleared the route of vehicle", name, id)
	case actionCreate:
		return withSubject("created "+label, name, id)
	case actionUpdate:
		return withSubject("updated "+label, name, id)
	case actionDelete:
		return withSubject("deleted "+label, name, id)
	}

	return action
}

// withSubject finishes a sentence with the name of the object, or with its
// id when the name is not known.
func withSubject(prefix, name string, id *int64) string {
	if name != "" {
		return fmt.Sprintf("%s %q", prefix, name)
	}
	if id != nil {
		return fmt.Sprintf("%s #%d", prefix, *id)
	}
	return prefix
}

// subjectFromResponse digs the name of the object out of a JSON answer. The
// shapes differ per handler (a zone nests its name in "properties", an
// account calls it "username"), so every known spelling is tried.
//
// An account is named after its handle: the display name of an account can
// be empty, and two accounts may share a display name.
func subjectFromResponse(body []byte, entityType string) string {
	var envelope struct {
		Data struct {
			Name       string `json:"name"`
			Code       string `json:"code"`
			Username   string `json:"username"`
			Title      string `json:"title"`
			Properties struct {
				Name string `json:"name"`
			} `json:"properties"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}

	d := envelope.Data
	if entityType == entityUser {
		if d.Username != "" {
			return d.Username
		}
		return d.Name
	}

	switch {
	case d.Name != "":
		return d.Name
	case d.Properties.Name != "":
		return d.Properties.Name
	case d.Username != "":
		return d.Username
	case d.Code != "":
		return d.Code
	case d.Title != "":
		// A work order is named after its title.
		return d.Title
	}
	return ""
}

// idFromResponse reads the id of the object the answer talks about.
func idFromResponse(body []byte) (int64, bool) {
	var envelope struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, false
	}
	return envelope.Data.ID, envelope.Data.ID > 0
}

// importedFromResponse reads how many zones an import brought in.
func importedFromResponse(body []byte) int {
	var envelope struct {
		Data struct {
			Imported int `json:"imported"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0
	}
	return envelope.Data.Imported
}

// subjectName reads the name of a row that is about to disappear.
func (r *Recorder) subjectName(ctx context.Context, entityType string, id *int64) string {
	if id == nil {
		return ""
	}
	// A route is named after the vehicle it belongs to.
	if entityType == entityRoute {
		entityType = entityVehicle
	}
	query, ok := subjectQueries[entityType]
	if !ok {
		return ""
	}

	var name string
	if err := r.db.QueryRow(ctx, query, *id).Scan(&name); err != nil {
		return ""
	}
	return name
}

// actorID reads the id of the account that made the request.
func actorID(c *gin.Context) *int64 {
	raw := auth.UserID(c)
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	return &id
}

// actorEmail reads the identity of the account that made the request. An
// account created with a username only has no address, so the username is
// the identity then.
func actorEmail(c *gin.Context) string {
	email := strings.TrimSpace(auth.UserEmail(c))
	if email == "" {
		email = strings.TrimSpace(auth.UserName(c))
	}
	return email
}

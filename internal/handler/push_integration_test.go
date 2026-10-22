//go:build integration

// Integration tests for the Web Push feature: the subscriptions of the
// browsers, the test message, and the three events that reach the operator
// (a unit that departs, a unit that arrives, a work order that is assigned).
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and the migrations applied.
//
// Run: go test -tags integration ./internal/handler/ -v -run Push
//
// Nothing here talks to a real push service: every delivery goes through a
// fake sender, so the test never leaves the machine.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/push"
	"entity-management/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// fakePushDelivery is one message the fake sender was asked to deliver.
type fakePushDelivery struct {
	UserID   int64
	Endpoint string
	Payload  push.Payload
}

// fakePushSender records the messages and answers what the test asks for.
//
// The endpoints listed in goneFor are the ones the push service does not
// know anymore: it answers 410 for those.
type fakePushSender struct {
	mu      sync.Mutex
	sent    []fakePushDelivery
	goneFor map[string]bool
}

func (f *fakePushSender) Send(ctx context.Context, sub push.Subscription, payload push.Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sent = append(f.sent, fakePushDelivery{
		UserID:   sub.UserID,
		Endpoint: sub.Endpoint,
		Payload:  payload,
	})

	if f.goneFor[sub.Endpoint] {
		return fmt.Errorf("%w: the push service answered 410", push.ErrGone)
	}
	return nil
}

// deliveries returns the messages that were delivered so far.
func (f *fakePushSender) deliveries() []fakePushDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePushDelivery(nil), f.sent...)
}

// forget makes the push service answer 410 for an endpoint.
func (f *fakePushSender) forget(endpoint string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.goneFor == nil {
		f.goneFor = map[string]bool{}
	}
	f.goneFor[endpoint] = true
}

// waitFor waits until the sender has recorded n messages.
//
// The pushes leave the server in a goroutine of their own, so a test that
// wants to see one has to wait for it.
func (f *fakePushSender) waitFor(t *testing.T, n int) []fakePushDelivery {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got := f.deliveries()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d messages arrived, want %d", len(got), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pushFixture is the push handler, the database, and a router with the push
// endpoints and the three triggers behind the auth middleware.
type pushFixture struct {
	pool    *db.Pool
	router  *gin.Engine
	service *auth.Service
	push    *push.Service
	sender  *fakePushSender
}

// pushFixtureFor builds the fixture and cleans the leftovers of an earlier
// run.
func pushFixtureFor(t *testing.T) *pushFixture {
	t.Helper()

	if err := godotenv.Load("../../.env"); err != nil {
		t.Fatalf("load .env: %v (run from repo root with DB_PASSWORD set)", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pool, err := db.NewPool(&cfg.Database)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pushCleanup(ctx, pool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pushCleanup(ctx, pool)
	})

	gin.SetMode(gin.TestMode)
	service := auth.NewServiceWithRefresh(pool, "groundwork-push-test-secret", time.Hour, time.Hour)
	mw := auth.NewMiddleware(service)

	sender := &fakePushSender{}
	pushService := push.NewService(pool, push.Config{
		PublicKey:  "test-public-key",
		PrivateKey: "test-private-key",
		Subject:    "mailto:test@localhost",
	}, sender)

	router := gin.New()
	protected := router.Group("/api", mw.Require())

	pushHandler := NewPushHandler(pushService)
	protected.GET("/push/vapid-public-key", pushHandler.PublicKey)
	protected.POST("/push/subscribe", pushHandler.Subscribe)
	protected.DELETE("/push/subscribe", pushHandler.Unsubscribe)
	protected.POST("/push/test", pushHandler.Test)

	// The three triggers.
	protected.POST("/vehicles/:id/route/start", NewRouteHandler(pool, ws.NewHub(), pushService).Start)
	protected.POST("/vehicles/:id/position", NewVehicleHandler(pool, ws.NewHub(), pushService).UpdatePosition)
	protected.POST("/tasks", NewTaskHandler(pool, pushService).Create)

	return &pushFixture{
		pool:    pool,
		router:  router,
		service: service,
		push:    pushService,
		sender:  sender,
	}
}

// pushCleanup removes everything the tests wrote.
func pushCleanup(ctx context.Context, pool *db.Pool) {
	pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint LIKE 'https://push.test/%'`)
	pool.Exec(ctx, `DELETE FROM tasks WHERE title LIKE 'pushtest-%'`)
	pool.Exec(ctx, `DELETE FROM vehicle_positions WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'pushtest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_routes WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'pushtest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'pushtest-%'`)
	pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'pushtest-%'`)
}

// seedPushUser creates an account and returns its id.
func seedPushUser(t *testing.T, pool *db.Pool, username, role string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash, role, name)
        VALUES ($1, $2, 'pushtest', $3, $1)
        RETURNING id`, username, username+"@pushtest.local", role).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}

	return id
}

// seedPushVehicle creates a unit and returns its id.
func seedPushVehicle(t *testing.T, pool *db.Pool, code string, driverID *int64) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO vehicles (code, name, kind, status, driver_id)
        VALUES ($1, $1, 'truck', 'idle', $2)
        RETURNING id`, code, driverID).Scan(&id)
	if err != nil {
		t.Fatalf("insert vehicle %s: %v", code, err)
	}

	return id
}

// seedPushRoute writes an active route of a unit. The line runs from
// (0, 0) to (0.01, 0), so the unit that reports the last point of the line
// has arrived.
func seedPushRoute(t *testing.T, pool *db.Pool, vehicleID int64, destName string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO vehicle_routes
            (vehicle_id, origin_name, dest_name, origin_lat, origin_lng, dest_lat, dest_lng,
             geom, distance_m, duration_s, source, status)
        VALUES ($1, 'Gudang', $2, 0, 0, 0, 0.01,
                ST_GeomFromText('LINESTRING(0 0, 0.01 0)', 4326), 1113, 60, 'direct', 'active')
        RETURNING id`, vehicleID, destName).Scan(&id)
	if err != nil {
		t.Fatalf("insert route: %v", err)
	}

	return id
}

// pushToken signs a token for an account.
func pushToken(t *testing.T, f *pushFixture, id int64, username, role string) string {
	t.Helper()

	session, err := f.service.NewSession(auth.User{ID: id, Username: username, Role: role})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return session.AccessToken
}

// pushRequest calls an endpoint with a token and a body.
func pushRequest(t *testing.T, f *pushFixture, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the body: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	return rec
}

// subscriptionBody is the body the browser hands over.
func subscriptionBody(endpoint string) map[string]any {
	return map[string]any{
		"endpoint": endpoint,
		"keys": map[string]any{
			"p256dh": "public-key-of-the-browser",
			"auth":   "secret-of-the-browser",
		},
	}
}

// countSubscriptions counts the rows of one endpoint.
func countSubscriptions(t *testing.T, pool *db.Pool, endpoint string) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM push_subscriptions WHERE endpoint = $1`, endpoint).Scan(&n)
	if err != nil {
		t.Fatalf("count the subscriptions: %v", err)
	}
	return n
}

// subscriptionOwner reads the account a subscription belongs to.
func subscriptionOwner(t *testing.T, pool *db.Pool, endpoint string) (int64, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx,
		`SELECT user_id FROM push_subscriptions WHERE endpoint = $1`, endpoint).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}

// The subscription is stored under the account that signed the request.
func TestPushSubscribeStoresTheSubscriptionOfTheSignedInAccount(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	token := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)

	endpoint := "https://push.test/admin-one"
	rec := pushRequest(t, f, http.MethodPost, "/api/push/subscribe", token, subscriptionBody(endpoint))
	if rec.Code != http.StatusOK {
		t.Fatalf("the subscribe answered %d: %s", rec.Code, rec.Body.String())
	}

	owner, ok := subscriptionOwner(t, f.pool, endpoint)
	if !ok {
		t.Fatal("the subscription was not stored")
	}
	if owner != admin {
		t.Errorf("the subscription belongs to account %d, want %d", owner, admin)
	}
}

// A browser that subscribes twice keeps one row.
func TestPushSubscribeTwiceKeepsOneRow(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	token := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)

	endpoint := "https://push.test/twice"
	for i := 0; i < 2; i++ {
		rec := pushRequest(t, f, http.MethodPost, "/api/push/subscribe", token, subscriptionBody(endpoint))
		if rec.Code != http.StatusOK {
			t.Fatalf("the subscribe answered %d: %s", rec.Code, rec.Body.String())
		}
	}

	if n := countSubscriptions(t, f.pool, endpoint); n != 1 {
		t.Errorf("the endpoint has %d rows, want 1", n)
	}
}

// The same browser, another account: the row follows the new owner.
func TestPushSubscribeMovesTheRowToTheNewOwner(t *testing.T) {
	f := pushFixtureFor(t)

	first := seedPushUser(t, f.pool, "pushtest-first", auth.RoleAdmin)
	second := seedPushUser(t, f.pool, "pushtest-second", auth.RoleAdmin)

	endpoint := "https://push.test/moves"
	firstToken := pushToken(t, f, first, "pushtest-first", auth.RoleAdmin)
	secondToken := pushToken(t, f, second, "pushtest-second", auth.RoleAdmin)

	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", firstToken, subscriptionBody(endpoint))
	rec := pushRequest(t, f, http.MethodPost, "/api/push/subscribe", secondToken, subscriptionBody(endpoint))
	if rec.Code != http.StatusOK {
		t.Fatalf("the subscribe answered %d: %s", rec.Code, rec.Body.String())
	}

	if n := countSubscriptions(t, f.pool, endpoint); n != 1 {
		t.Errorf("the endpoint has %d rows, want 1", n)
	}
	owner, ok := subscriptionOwner(t, f.pool, endpoint)
	if !ok {
		t.Fatal("the subscription was not stored")
	}
	if owner != second {
		t.Errorf("the subscription belongs to account %d, want %d", owner, second)
	}
}

// A body that is not a subscription is refused with the error format of the
// API.
func TestPushSubscribeRejectsABadBody(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	token := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)

	cases := map[string]map[string]any{
		"an empty endpoint": {
			"endpoint": "",
			"keys":     map[string]any{"p256dh": "p", "auth": "a"},
		},
		"an endpoint that is not https": {
			"endpoint": "http://push.test/nope",
			"keys":     map[string]any{"p256dh": "p", "auth": "a"},
		},
		"the keys are missing": {
			"endpoint": "https://push.test/no-keys",
		},
	}

	for name, body := range cases {
		rec := pushRequest(t, f, http.MethodPost, "/api/push/subscribe", token, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: the subscribe answered %d, want 400", name, rec.Code)
			continue
		}
		var answer struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("%s: decode the error: %v", name, err)
		}
		if answer.Error.Code != "validation_failed" {
			t.Errorf("%s: the error code is %q, want \"validation_failed\"", name, answer.Error.Code)
		}
	}
}

// A subscription is only removed by the account that owns it.
func TestPushUnsubscribeRemovesOnlyItsOwn(t *testing.T) {
	f := pushFixtureFor(t)

	first := seedPushUser(t, f.pool, "pushtest-first", auth.RoleAdmin)
	second := seedPushUser(t, f.pool, "pushtest-second", auth.RoleAdmin)

	firstToken := pushToken(t, f, first, "pushtest-first", auth.RoleAdmin)
	secondToken := pushToken(t, f, second, "pushtest-second", auth.RoleAdmin)

	mine := "https://push.test/mine"
	theirs := "https://push.test/theirs"
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", firstToken, subscriptionBody(mine))
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", secondToken, subscriptionBody(theirs))

	// The endpoint of somebody else stays where it is.
	rec := pushRequest(t, f, http.MethodDelete, "/api/push/subscribe", firstToken,
		map[string]any{"endpoint": theirs})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the unsubscribe answered %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := subscriptionOwner(t, f.pool, theirs); !ok {
		t.Error("the subscription of another account was removed")
	}

	// Its own subscription goes.
	rec = pushRequest(t, f, http.MethodDelete, "/api/push/subscribe", firstToken,
		map[string]any{"endpoint": mine})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the unsubscribe answered %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := subscriptionOwner(t, f.pool, mine); ok {
		t.Error("the own subscription was not removed")
	}
}

// The test message reaches the subscriptions of the caller only.
func TestPushTestSendsOnlyToTheCaller(t *testing.T) {
	f := pushFixtureFor(t)

	caller := seedPushUser(t, f.pool, "pushtest-caller", auth.RoleAdmin)
	other := seedPushUser(t, f.pool, "pushtest-other", auth.RoleAdmin)

	callerToken := pushToken(t, f, caller, "pushtest-caller", auth.RoleAdmin)
	otherToken := pushToken(t, f, other, "pushtest-other", auth.RoleAdmin)

	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", callerToken,
		subscriptionBody("https://push.test/caller"))
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", otherToken,
		subscriptionBody("https://push.test/other"))

	rec := pushRequest(t, f, http.MethodPost, "/api/push/test", callerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the test push answered %d: %s", rec.Code, rec.Body.String())
	}

	var answer struct {
		Data struct {
			Sent    int  `json:"sent"`
			Enabled bool `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	if answer.Data.Sent != 1 {
		t.Errorf("%d messages were sent, want 1", answer.Data.Sent)
	}
	if !answer.Data.Enabled {
		t.Error("the feature says it is off")
	}

	deliveries := f.sender.deliveries()
	if len(deliveries) != 1 {
		t.Fatalf("%d messages were delivered, want 1", len(deliveries))
	}
	if deliveries[0].UserID != caller {
		t.Errorf("the message went to account %d, want %d", deliveries[0].UserID, caller)
	}
	if deliveries[0].Payload.Title != "Notifikasi tes Groundwork" {
		t.Errorf("the title is %q, want \"Notifikasi tes Groundwork\"", deliveries[0].Payload.Title)
	}
}

// A subscription the push service no longer knows about is removed.
func TestPushGoneSubscriptionIsRemoved(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	token := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)

	endpoint := "https://push.test/gone"
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", token, subscriptionBody(endpoint))
	f.sender.forget(endpoint)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sent := f.push.NotifyUser(ctx, admin, push.Payload{Title: "Unit tiba"})
	if sent != 0 {
		t.Errorf("%d messages were counted as sent, want none", sent)
	}
	if _, ok := subscriptionOwner(t, f.pool, endpoint); ok {
		t.Error("the dead subscription was not removed")
	}
}

// The public key of the server is handed out, and the feature says whether
// it is on.
func TestPushVapidPublicKey(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	token := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)

	rec := pushRequest(t, f, http.MethodGet, "/api/push/vapid-public-key", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the key answered %d: %s", rec.Code, rec.Body.String())
	}

	var answer struct {
		Data struct {
			PublicKey string `json:"public_key"`
			Enabled   bool   `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	if !answer.Data.Enabled {
		t.Error("the feature says it is off")
	}
	if answer.Data.PublicKey == "" {
		t.Error("the public key is empty")
	}
}

// A unit that departs is announced to the admins and the staff — and to
// nobody else. The driver who pressed the button is left out.
func TestPushDepartureReachesTheOperatorsOnly(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	staff := seedPushUser(t, f.pool, "pushtest-staff", auth.RoleStaff)
	driver := seedPushUser(t, f.pool, "pushtest-driver", auth.RoleDriver)

	adminToken := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)
	staffToken := pushToken(t, f, staff, "pushtest-staff", auth.RoleStaff)
	driverToken := pushToken(t, f, driver, "pushtest-driver", auth.RoleDriver)

	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", adminToken,
		subscriptionBody("https://push.test/admin"))
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", staffToken,
		subscriptionBody("https://push.test/staff"))
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", driverToken,
		subscriptionBody("https://push.test/driver"))

	vehicle := seedPushVehicle(t, f.pool, "pushtest-unit", &driver)
	routeID := seedPushRoute(t, f.pool, vehicle, "Gudang Cisitu")

	rec := pushRequest(t, f, http.MethodPost,
		"/api/vehicles/"+itoa(vehicle)+"/route/start", driverToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", rec.Code, rec.Body.String())
	}

	// Wait for at least the two test-created operators. Other admins/staff
	// may exist in the database and also receive the push.
	deliveries := f.sender.waitFor(t, 2)

	receivers := map[int64]bool{}
	for _, d := range deliveries {
		receivers[d.UserID] = true
	}
	// The test-created admin and staff must receive the notification.
	if !receivers[admin] || !receivers[staff] {
		t.Errorf("the messages went to %v, want at least the admin and the staff", receivers)
	}
	// The driver who triggered the event must not receive it.
	if receivers[driver] {
		t.Error("the driver was told about their own departure")
	}

	// All deliveries must have the correct payload.
	for _, d := range deliveries {
		if d.Payload.Title != "Unit berangkat" {
			t.Errorf("the title is %q, want \"Unit berangkat\"", d.Payload.Title)
		}
		if d.Payload.Tag != fmt.Sprintf("route-started-%d", routeID) {
			t.Errorf("the tag is %q, want \"route-started-%d\"", d.Payload.Tag, routeID)
		}
		if !strings.Contains(d.Payload.Body, "pushtest-unit") {
			t.Errorf("the body %q does not name the unit", d.Payload.Body)
		}
		if !strings.Contains(d.Payload.Body, "Gudang Cisitu") {
			t.Errorf("the body %q does not name the destination", d.Payload.Body)
		}
	}
}

// A unit that arrives is announced once, not on every position it reports.
func TestPushArrivalIsAnnouncedOnce(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	adminToken := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", adminToken,
		subscriptionBody("https://push.test/admin"))

	vehicle := seedPushVehicle(t, f.pool, "pushtest-unit", nil)
	routeID := seedPushRoute(t, f.pool, vehicle, "Gudang Cisitu")

	// The last point of the line: the unit has arrived.
	body := map[string]any{"lat": 0, "lng": 0.01, "speed_kmh": 0}
	rec := pushRequest(t, f, http.MethodPost,
		"/api/vehicles/"+itoa(vehicle)+"/position", adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("the position answered %d: %s", rec.Code, rec.Body.String())
	}

	// Wait for at least the test-created admin. Other admins/staff may exist
	// in the database and also receive the push.
	deliveries := f.sender.waitFor(t, 1)

	// The test-created admin must receive the notification.
	found := false
	for _, d := range deliveries {
		if d.UserID == admin {
			found = true
			if d.Payload.Title != "Unit tiba" {
				t.Errorf("the title is %q, want \"Unit tiba\"", d.Payload.Title)
			}
			if d.Payload.Tag != fmt.Sprintf("route-completed-%d", routeID) {
				t.Errorf("the tag is %q, want \"route-completed-%d\"", d.Payload.Tag, routeID)
			}
			if !strings.Contains(d.Payload.Body, "Gudang Cisitu") {
				t.Errorf("the body %q does not name the destination", d.Payload.Body)
			}
			break
		}
	}
	if !found {
		t.Errorf("the test-created admin (id=%d) did not receive the arrival notification", admin)
	}

	// The route is closed now: the next report announces nothing.
	rec = pushRequest(t, f, http.MethodPost,
		"/api/vehicles/"+itoa(vehicle)+"/position", adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("the second position answered %d: %s", rec.Code, rec.Body.String())
	}
	time.Sleep(300 * time.Millisecond)
	// The total delivery count must not have increased.
	if got := len(f.sender.deliveries()); got != len(deliveries) {
		t.Errorf("%d messages were delivered after second report, want %d (no new delivery)", got, len(deliveries))
	}
}

// A work order reaches the account it was assigned to, and nobody else.
func TestPushTaskReachesTheAssignedAccountOnly(t *testing.T) {
	f := pushFixtureFor(t)

	admin := seedPushUser(t, f.pool, "pushtest-admin", auth.RoleAdmin)
	driver := seedPushUser(t, f.pool, "pushtest-driver", auth.RoleDriver)

	adminToken := pushToken(t, f, admin, "pushtest-admin", auth.RoleAdmin)
	driverToken := pushToken(t, f, driver, "pushtest-driver", auth.RoleDriver)

	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", adminToken,
		subscriptionBody("https://push.test/admin"))
	pushRequest(t, f, http.MethodPost, "/api/push/subscribe", driverToken,
		subscriptionBody("https://push.test/driver"))

	body := map[string]any{
		"title":            "pushtest-task",
		"assigned_user_id": driver,
	}
	rec := pushRequest(t, f, http.MethodPost, "/api/tasks", adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the task answered %d: %s", rec.Code, rec.Body.String())
	}

	deliveries := f.sender.waitFor(t, 1)
	if len(deliveries) != 1 {
		t.Fatalf("%d messages were delivered, want 1", len(deliveries))
	}
	if deliveries[0].UserID != driver {
		t.Errorf("the message went to account %d, want %d (the assigned driver)",
			deliveries[0].UserID, driver)
	}
	if deliveries[0].Payload.Title != "Tugas baru" {
		t.Errorf("the title is %q, want \"Tugas baru\"", deliveries[0].Payload.Title)
	}
	if !strings.Contains(deliveries[0].Payload.Body, "pushtest-task") {
		t.Errorf("the body %q does not name the task", deliveries[0].Payload.Body)
	}
	if deliveries[0].Payload.URL != "/driver" {
		t.Errorf("the link is %q, want \"/driver\"", deliveries[0].Payload.URL)
	}
}

package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// request builds a Gin context for one request, so the mapping can be
// checked without a database.
func request(method, path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, path, nil)
	return c
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		action     string
		entityType string
		entityID   int64
		ok         bool
	}{
		{
			name: "a read is not a change", method: http.MethodGet,
			path: "/api/zones", ok: false,
		},
		{
			name: "a new zone", method: http.MethodPost,
			path: "/api/zones", action: actionCreate, entityType: entityZone, ok: true,
		},
		{
			name: "a changed zone", method: http.MethodPut,
			path: "/api/zones/12", action: actionUpdate, entityType: entityZone, entityID: 12, ok: true,
		},
		{
			name: "a removed zone", method: http.MethodDelete,
			path: "/api/zones/12", action: actionDelete, entityType: entityZone, entityID: 12, ok: true,
		},
		{
			name: "a removed place", method: http.MethodDelete,
			path: "/api/entities/7", action: actionDelete, entityType: entityEntity, entityID: 7, ok: true,
		},
		{
			name: "a new account", method: http.MethodPost,
			path: "/api/users", action: actionCreate, entityType: entityUser, ok: true,
		},
		{
			name: "a changed account", method: http.MethodPut,
			path: "/api/users/5", action: actionUpdate, entityType: entityUser, entityID: 5, ok: true,
		},
		{
			name: "an imported zone collection", method: http.MethodPost,
			path: "/api/zones/import", action: actionImport, entityType: entityZone, ok: true,
		},
		{
			name: "a zone made from a region", method: http.MethodPost,
			path: "/api/zones/from-region", action: actionCreate, entityType: entityZone, ok: true,
		},
		{
			name: "a planned route", method: http.MethodPost,
			path: "/api/vehicles/3/route", action: actionSet, entityType: entityRoute, entityID: 3, ok: true,
		},
		{
			name: "a cleared route", method: http.MethodDelete,
			path: "/api/vehicles/3/route", action: actionClear, entityType: entityRoute, entityID: 3, ok: true,
		},
		{
			name: "a position report is telemetry", method: http.MethodPost,
			path: "/api/vehicles/3/position", ok: false,
		},
		{
			name: "something outside the api", method: http.MethodPost,
			path: "/health", ok: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, entityType, entityID, ok := describe(request(tc.method, tc.path))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if action != tc.action {
				t.Errorf("action = %q, want %q", action, tc.action)
			}
			if entityType != tc.entityType {
				t.Errorf("entityType = %q, want %q", entityType, tc.entityType)
			}
			if tc.entityID == 0 {
				if entityID != nil {
					t.Errorf("entityID = %v, want none", *entityID)
				}
				return
			}
			if entityID == nil {
				t.Fatalf("entityID = none, want %d", tc.entityID)
			}
			if *entityID != tc.entityID {
				t.Errorf("entityID = %d, want %d", *entityID, tc.entityID)
			}
		})
	}
}

func TestSubjectFromResponse(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		entityType string
		want       string
	}{
		{
			name: "a place", body: `{"data":{"id":1,"name":"Monas"}}`,
			entityType: entityEntity, want: "Monas",
		},
		{
			name: "a zone nests its name", body: `{"data":{"type":"Feature","id":3,"properties":{"name":"Gudang"}}}`,
			entityType: entityZone, want: "Gudang",
		},
		{
			name: "a vehicle", body: `{"data":{"id":4,"code":"TRK-001","name":"Truck Simulator 1"}}`,
			entityType: entityVehicle, want: "Truck Simulator 1",
		},
		{
			name: "an account is named after its handle", body: `{"data":{"id":5,"username":"budi","name":"Budi Santoso"}}`,
			entityType: entityUser, want: "budi",
		},
		{
			name: "an answer without a name", body: `{"data":{"count":2}}`,
			entityType: entityZone, want: "",
		},
		{
			name: "something that is not JSON", body: `not json`,
			entityType: entityZone, want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subjectFromResponse([]byte(tc.body), tc.entityType); got != tc.want {
				t.Errorf("subject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetails(t *testing.T) {
	id := int64(12)

	cases := []struct {
		name       string
		action     string
		entityType string
		subject    string
		entityID   *int64
		imported   int
		want       string
	}{
		{
			name: "a created zone", action: actionCreate, entityType: entityZone,
			subject: "Gudang", want: `created zone "Gudang"`,
		},
		{
			name: "a deleted zone without a name", action: actionDelete, entityType: entityZone,
			entityID: &id, want: "deleted zone #12",
		},
		{
			name: "a planned route", action: actionSet, entityType: entityRoute,
			subject: "Truck Simulator 1", want: `planned a route for vehicle "Truck Simulator 1"`,
		},
		{
			name: "an import", action: actionImport, entityType: entityZone,
			imported: 12, want: "imported 12 zone(s)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := details(tc.action, tc.entityType, tc.subject, tc.entityID, tc.imported)
			if got != tc.want {
				t.Errorf("details = %q, want %q", got, tc.want)
			}
		})
	}
}

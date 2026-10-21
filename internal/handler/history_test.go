package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// historyContext builds a request with the given query parameters.
func historyContext(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/api/vehicles/1/history"+query, nil)
	c.Request = req
	return c
}

func TestHistoryRangeDate(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 4, 5, 0, time.UTC)

	from, to, err := historyRange(historyContext("?date=2026-10-21"), now)
	if err != nil {
		t.Fatalf("historyRange: %v", err)
	}
	if want := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Fatalf("from = %s, want %s", from, want)
	}
	if want := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC); !to.Equal(want) {
		t.Fatalf("to = %s, want %s", to, want)
	}
}

func TestHistoryRangeDefaultsToToday(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 4, 5, 0, time.UTC)

	from, to, err := historyRange(historyContext(""), now)
	if err != nil {
		t.Fatalf("historyRange: %v", err)
	}
	if want := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Fatalf("from = %s, want %s", from, want)
	}
	if !to.Equal(from.AddDate(0, 0, 1)) {
		t.Fatalf("to = %s, want the next day", to)
	}
}

func TestHistoryRangeExplicitWindow(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 4, 5, 0, time.UTC)

	from, to, err := historyRange(historyContext("?from=2026-10-21T08:00:00Z&to=2026-10-21T12:00:00Z"), now)
	if err != nil {
		t.Fatalf("historyRange: %v", err)
	}
	if want := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Fatalf("from = %s, want %s", from, want)
	}
	if want := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC); !to.Equal(want) {
		t.Fatalf("to = %s, want %s", to, want)
	}
}

func TestHistoryRangeRejectsBadInput(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 4, 5, 0, time.UTC)

	cases := map[string]string{
		"a date that is not a date":   "?date=21-10-2026",
		"a range that ends first":     "?from=2026-10-21T12:00:00Z&to=2026-10-21T08:00:00Z",
		"a range that is too long":    "?from=2026-01-01T00:00:00Z&to=2026-10-21T08:00:00Z",
		"a timestamp that is not one": "?from=yesterday",
	}
	for name, query := range cases {
		if _, _, err := historyRange(historyContext(query), now); err == nil {
			t.Errorf("%s: expected the request to be refused", name)
		}
	}
}

func TestHistoryLimit(t *testing.T) {
	if got := historyLimit(historyContext("")); got != historyDefaultLimit {
		t.Fatalf("limit = %d, want %d", got, historyDefaultLimit)
	}
	if got := historyLimit(historyContext("?limit=500")); got != 500 {
		t.Fatalf("limit = %d, want 500", got)
	}
	if got := historyLimit(historyContext("?limit=999999")); got != historyMaxLimit {
		t.Fatalf("limit = %d, want the ceiling %d", got, historyMaxLimit)
	}
	if got := historyLimit(historyContext("?limit=nonsense")); got != historyDefaultLimit {
		t.Fatalf("limit = %d, want the default %d", got, historyDefaultLimit)
	}
}

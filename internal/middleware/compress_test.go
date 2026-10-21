package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGzipCompressesLargeBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := strings.Repeat(`{"name":"Monas","type":"monument"}`, 200)

	rec := httptest.NewRecorder()
	r := gin.New()
	r.Use(Gzip(DefaultCompressThreshold))
	r.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": payload}) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	r.ServeHTTP(rec, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q, want it to mention Accept-Encoding", res.Header.Get("Vary"))
	}

	reader, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Monas") {
		t.Fatalf("body does not carry the payload")
	}
	if rec.Body.Len() >= len(payload) {
		t.Fatalf("compressed body is %d bytes, payload is %d: nothing was saved", rec.Body.Len(), len(payload))
	}
}

func TestGzipLeavesSmallBodiesAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	r := gin.New()
	r.Use(Gzip(DefaultCompressThreshold))
	r.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r.ServeHTTP(rec, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none for a small body", got)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("body = %q, want the JSON answer", string(body))
	}
}

func TestGzipSkippedWhenClientDoesNotAcceptIt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := strings.Repeat("x", 4096)

	for _, accept := range []string{"", "deflate, br", "gzip;q=0"} {
		rec := httptest.NewRecorder()
		r := gin.New()
		r.Use(Gzip(DefaultCompressThreshold))
		r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, payload) })

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		r.ServeHTTP(rec, req)

		res := rec.Result()
		if got := res.Header.Get("Content-Encoding"); got != "" {
			t.Fatalf("Accept-Encoding %q: Content-Encoding = %q, want none", accept, got)
		}
		body, _ := io.ReadAll(res.Body)
		if string(body) != payload {
			t.Fatalf("Accept-Encoding %q: body was altered", accept)
		}
	}
}

func TestGzipSkipsWebSocketUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	r := gin.New()
	r.Use(Gzip(DefaultCompressThreshold))
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, strings.Repeat("y", 4096)) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Upgrade", "websocket")
	r.ServeHTTP(rec, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none for a websocket upgrade", got)
	}
}

func TestHeaderAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"gzip":                  true,
		"gzip, deflate, br":     true,
		"deflate, gzip":         true,
		"GZIP":                  true,
		"gzip;q=0.5":            true,
		"gzip;q=0":              false,
		"gzip;q=0.0":            false,
		"deflate":               false,
		"":                      false,
		"br, gzip;q=0.9":        true,
		"identity":              false,
		"gzip; q=1.0, identity": true,
	}
	for accept, want := range cases {
		if got := headerAcceptsGzip(accept); got != want {
			t.Errorf("headerAcceptsGzip(%q) = %v, want %v", accept, got, want)
		}
	}
}

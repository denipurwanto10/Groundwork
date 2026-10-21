// Package middleware holds the cross-cutting HTTP concerns of the server:
// response compression and the headers that go with it.
//
// The package is deliberately small. Everything that belongs to one resource
// lives in internal/handler; what belongs to every response lives here.
package middleware

import (
	"compress/gzip"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// DefaultCompressThreshold is the smallest body worth compressing. A JSON
// answer below it costs more in headers than it saves in bytes, and the
// health check is polled every few seconds.
const DefaultCompressThreshold = 1024

// Gzip compresses the response body when the client accepts gzip and the body
// is big enough to pay for it.
//
// The body is buffered until it passes the threshold, so a short answer is
// sent as it is. A long one — a zone with a hundred thousand vertices, the
// fleet list, a region collection — is compressed, which is where the
// difference shows: the same zone geometry shrinks to about a tenth of its
// size, and a map on a slow link stops waiting for it.
//
// WebSocket upgrades are left alone: the stream is not a response body and
// the connection is hijacked, not written.
func Gzip(minSize int) gin.HandlerFunc {
	if minSize < 0 {
		minSize = 0
	}
	return func(c *gin.Context) {
		if !acceptsGzip(c.Request) {
			c.Next()
			return
		}

		w := &gzipWriter{ResponseWriter: c.Writer, minSize: minSize}
		c.Writer = w
		c.Next()
		_ = w.Close()
	}
}

// acceptsGzip reports whether the client asked for gzip. A WebSocket
// handshake is never compressed: the connection is taken over afterwards.
func acceptsGzip(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	return headerAcceptsGzip(r.Header.Get("Accept-Encoding"))
}

// headerAcceptsGzip parses an Accept-Encoding header. "gzip;q=0" means the
// client refuses it, and a wildcard does not count unless gzip is named.
func headerAcceptsGzip(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		for _, param := range strings.Split(params, ";") {
			key, value, found := strings.Cut(strings.TrimSpace(param), "=")
			if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			// q=0 refuses the encoding; anything else accepts it.
			return strings.TrimSpace(value) != "0" && strings.TrimSpace(value) != "0.0"
		}
		return true
	}
	return false
}

// gzipWriter wraps a gin.ResponseWriter and compresses what goes through it.
//
// The header can only be written once, and the decision to compress depends
// on the size of the body, so the body is buffered until the threshold is
// passed. Only then are the headers written and the stream switched to gzip.
type gzipWriter struct {
	gin.ResponseWriter

	minSize int

	buf         []byte
	gz          *gzip.Writer
	decided     bool
	compressing bool
	passthrough bool
	closed      bool
	size        int
}

// Write buffers the body until the threshold is reached.
func (w *gzipWriter) Write(data []byte) (int, error) {
	if w.passthrough {
		return w.writeThrough(data)
	}

	if !w.decided {
		w.buf = append(w.buf, data...)
		if len(w.buf) >= w.minSize {
			w.startCompress()
		}
		return len(data), nil
	}

	n, err := w.gz.Write(data)
	w.size += n
	return n, err
}

// WriteString keeps the buffering behaviour for gin's string renderer.
func (w *gzipWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// WriteHeader records the status. The header itself is written later, when
// it is known whether the body is compressed.
func (w *gzipWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

// WriteHeaderNow defers the header write until the body decides whether it
// is compressed.
func (w *gzipWriter) WriteHeaderNow() {
	if w.decided {
		w.ResponseWriter.WriteHeaderNow()
	}
}

// startCompress switches the response to gzip. A response that already
// carries its own Content-Encoding is left alone.
func (w *gzipWriter) startCompress() {
	w.decided = true

	header := w.Header()
	if header.Get("Content-Encoding") != "" {
		w.passthrough = true
		w.writeBuffered()
		return
	}

	w.compressing = true
	header.Set("Content-Encoding", "gzip")
	header.Del("Content-Length")
	addVary(header, "Accept-Encoding")

	w.ResponseWriter.WriteHeaderNow()
	w.gz = gzip.NewWriter(w.ResponseWriter)
	if len(w.buf) > 0 {
		n, _ := w.gz.Write(w.buf)
		w.size += n
		w.buf = nil
	}
}

// writeThrough writes straight to the client, without compression.
func (w *gzipWriter) writeThrough(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.size += n
	return n, err
}

// writeBuffered sends what is still buffered, uncompressed.
func (w *gzipWriter) writeBuffered() {
	if len(w.buf) == 0 {
		return
	}
	_, _ = w.writeThrough(w.buf)
	w.buf = nil
}

// Flush sends what has been written so far. A response that flushes before
// it reaches the threshold is streamed as it is: the point of a flush is that
// the client gets the bytes now.
func (w *gzipWriter) Flush() {
	switch {
	case !w.decided:
		w.decided = true
		w.passthrough = true
		w.ResponseWriter.WriteHeaderNow()
		w.writeBuffered()
	case w.compressing && w.gz != nil:
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

// Close finishes the response: it writes the header and the body of a short
// answer, or the gzip trailer of a compressed one. Safe to call twice.
func (w *gzipWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	if !w.decided {
		w.decided = true
		w.passthrough = true
		w.ResponseWriter.WriteHeaderNow()
		w.writeBuffered()
		return nil
	}

	if w.compressing && w.gz != nil {
		err := w.gz.Close()
		w.gz = nil
		return err
	}
	return nil
}

// Size reports how many bytes went to the client.
func (w *gzipWriter) Size() int {
	return w.size
}

// Written reports whether a response was already sent.
func (w *gzipWriter) Written() bool {
	return w.decided || w.ResponseWriter.Written()
}

// Unwrap lets http.ResponseController reach the real writer, so the
// WebSocket handshake and the write deadlines keep working.
func (w *gzipWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// addVary appends a field name to the Vary header without repeating it.
func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

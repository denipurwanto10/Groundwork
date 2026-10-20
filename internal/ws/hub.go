// Package ws fans live vehicle positions out to every subscribed browser.
//
// The hub is deliberately transport-agnostic: a subscriber only has to
// implement Sink, which keeps the fan-out unit-testable and leaves the
// WebSocket upgrade, keepalive and framing to the HTTP layer.
package ws

import "sync"

// Sink is a single subscriber.
//
// Send must never block: a subscriber that cannot keep up has to be dropped by
// Broadcast instead of stalling the publisher, because the publisher is the
// position API running inside an HTTP request.
type Sink interface {
	// Send queues one frame. It returns an error when the subscriber is gone
	// or too slow to accept more frames.
	Send(frame []byte) error
	// Close releases the subscriber. It must be safe to call more than once.
	Close()
}

// Hub keeps the set of live subscribers and fans frames out to them.
type Hub struct {
	mu    sync.RWMutex
	sinks map[Sink]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{sinks: make(map[Sink]struct{})}
}

// Add registers a sink and returns the function that removes it again.
func (h *Hub) Add(s Sink) (remove func()) {
	h.mu.Lock()
	h.sinks[s] = struct{}{}
	h.mu.Unlock()

	return func() { h.Remove(s) }
}

// Remove unregisters a sink and closes it.
func (h *Hub) Remove(s Sink) {
	h.mu.Lock()
	_, ok := h.sinks[s]
	delete(h.sinks, s)
	h.mu.Unlock()

	if ok {
		s.Close()
	}
}

// Broadcast delivers the frame to every subscriber. Subscribers that fail to
// accept the frame are removed and closed.
func (h *Hub) Broadcast(frame []byte) {
	h.mu.RLock()
	sinks := make([]Sink, 0, len(h.sinks))
	for s := range h.sinks {
		sinks = append(sinks, s)
	}
	h.mu.RUnlock()

	for _, s := range sinks {
		if err := s.Send(frame); err != nil {
			h.Remove(s)
		}
	}
}

// Subscribers reports how many subscribers are currently connected.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.sinks)
}

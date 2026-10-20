package ws

import (
	"errors"
	"sync"
	"testing"
)

type fakeSink struct {
	mu     sync.Mutex
	frames [][]byte
	err    error
	closed bool
}

func (f *fakeSink) Send(frame []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}
	f.frames = append(f.frames, append([]byte(nil), frame...))

	return nil
}

func (f *fakeSink) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed = true
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.frames)
}

func (f *fakeSink) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.closed
}

func TestBroadcastReachesEverySubscriber(t *testing.T) {
	hub := NewHub()
	a, b := &fakeSink{}, &fakeSink{}
	hub.Add(a)
	hub.Add(b)

	hub.Broadcast([]byte(`{"type":"position"}`))

	if a.count() != 1 || b.count() != 1 {
		t.Fatalf("expected both subscribers to receive the frame, got %d and %d", a.count(), b.count())
	}
	if hub.Subscribers() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", hub.Subscribers())
	}
}

func TestBroadcastDropsFailingSubscriber(t *testing.T) {
	hub := NewHub()
	broken := &fakeSink{err: errors.New("too slow")}
	healthy := &fakeSink{}
	hub.Add(broken)
	hub.Add(healthy)

	hub.Broadcast([]byte("frame"))

	if hub.Subscribers() != 1 {
		t.Fatalf("expected the failing subscriber to be dropped, got %d subscribers", hub.Subscribers())
	}
	if !broken.isClosed() {
		t.Fatal("expected the failing subscriber to be closed")
	}
	if healthy.count() != 1 {
		t.Fatal("expected the healthy subscriber to still receive the frame")
	}
}

func TestRemoveClosesSubscriber(t *testing.T) {
	hub := NewHub()
	sink := &fakeSink{}
	remove := hub.Add(sink)

	remove()

	if hub.Subscribers() != 0 {
		t.Fatalf("expected 0 subscribers, got %d", hub.Subscribers())
	}
	if !sink.isClosed() {
		t.Fatal("expected the subscriber to be closed")
	}

	// Removing twice must not panic.
	remove()
}

func TestBroadcastToEmptyHub(t *testing.T) {
	hub := NewHub()
	hub.Broadcast([]byte("frame"))

	if hub.Subscribers() != 0 {
		t.Fatalf("expected 0 subscribers, got %d", hub.Subscribers())
	}
}

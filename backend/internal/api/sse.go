package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// SSEBroker manages Server-Sent Events connections.
type SSEBroker struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}

	// done is closed by Close, ending every open stream.
	done      chan struct{}
	closeOnce sync.Once
}

// SSEEvent represents a single SSE event to send to clients.
type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// NewSSEBroker creates a new SSE broker.
func NewSSEBroker() *SSEBroker {
	return &SSEBroker{
		clients: make(map[chan SSEEvent]struct{}),
		done:    make(chan struct{}),
	}
}

// Close ends every open stream, and any opened after.
//
// A stream stays open for as long as the page does, and http.Server.Shutdown
// waits for open requests to finish — so without this, every shutdown with a
// browser tab open waited out its whole timeout and then gave up.
func (b *SSEBroker) Close() {
	b.closeOnce.Do(func() { close(b.done) })
}

// Subscribe adds a new client and returns its event channel.
func (b *SSEBroker) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a client.
func (b *SSEBroker) Unsubscribe(ch chan SSEEvent) {
	b.mu.Lock()
	delete(b.clients, ch)
	close(ch)
	b.mu.Unlock()
}

// Publish sends an event to all connected clients.
func (b *SSEBroker) Publish(event SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			// Client is slow, skip this event
		}
	}
}

// ServeHTTP handles SSE connections.
func (b *SSEBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Clear the server's write deadline for this connection. An event stream is
	// meant to stay open for the length of a migration, but the server-wide
	// WriteTimeout applies to it like any other response and was closing every
	// stream after a minute — dropping any event published before the client
	// reconnected, since there is no replay.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		log.Printf("[warn] could not clear SSE write deadline: %v", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// Send initial connected event
	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}
}

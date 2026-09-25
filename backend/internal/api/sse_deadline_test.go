package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A stream has to outlive the server's write timeout. The SSE handler lifts
// the timeout for its own connection, but it does so through the middleware's
// response wrapper — which, without Unwrap, refused, so every stream was cut
// after WriteTimeout and events published before the reconnect were lost.
//
// This runs the real router on a real server with a short WriteTimeout, and
// publishes only after that timeout has passed.
func TestEventStreamOutlivesTheWriteTimeout(t *testing.T) {
	s := NewServer()
	srv := httptest.NewUnstartedServer(newRouterWithServer(s, nil))
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	req.Host = "127.0.0.1:8080" // the origin guard only answers to loopback
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// Well past the write timeout.
	time.Sleep(400 * time.Millisecond)
	s.SSE.Publish(SSEEvent{Type: "rate_limit", Data: map[string]any{"waiting": true}})

	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "rate_limit") {
				got <- sc.Text()
				return
			}
		}
		got <- ""
	}()

	select {
	case line := <-got:
		if line == "" {
			t.Fatal("the stream was closed before the event arrived — the write timeout still applies to it")
		}
	case <-ctx.Done():
		t.Fatal("no event arrived")
	}
}

// Ctrl-C with a browser tab open waited out the whole shutdown timeout and
// then failed: Shutdown waits for open requests, and an event stream never
// finishes by itself.
func TestShutdownEndsOpenEventStreams(t *testing.T) {
	s := NewServer()
	srv := httptest.NewUnstartedServer(newRouterWithServer(s, nil))
	srv.Config.RegisterOnShutdown(s.SSE.Close)
	srv.Start()
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	req.Host = "127.0.0.1:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v after %s — the open stream held it", err, time.Since(start))
	}
}

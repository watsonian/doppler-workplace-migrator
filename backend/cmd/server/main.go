package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/doppler/onprem-migrator/internal/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	addr := fmt.Sprintf("%s:%s", host, port)

	router, apiServer := api.NewRouterAndServer()

	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Shutdown waits for open requests, and an event stream is one that never
	// finishes by itself. Ending the streams when shutdown begins lets it
	// complete instead of waiting out its timeout.
	srv.RegisterOnShutdown(apiServer.SSE.Close)

	if apiServer.ResumeFromEnvironment() {
		log.Println("Resumed the migration waiting to cut over, with the tokens from the environment")
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server starting on %s", browsableURL(host, port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-done
	log.Println("Server shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown failed: %v", err)
	}

	// Flush any checkpoint state a running migration has not persisted yet. The
	// background writer debounces to once a second, so without this up to a
	// second of progress is lost on shutdown — and that progress is what a resume
	// depends on.
	apiServer.Shutdown()

	log.Println("Server stopped")
}

// browsableURL renders the listen address as something a terminal will linkify
// and a browser will actually open.
//
// The scheme is what makes it clickable. The host substitution is what stops the
// click being a dead end: in Docker the server binds 0.0.0.0, and a browser
// handed http://0.0.0.0:8080 resolves it inconsistently — usually to loopback on
// Linux, often to nothing on macOS. The bind address is a server-side detail;
// the address a person should open is localhost either way.
func browsableURL(host, port string) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

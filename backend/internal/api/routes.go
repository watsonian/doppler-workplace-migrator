package api

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	backend "github.com/doppler/workplace-migrator"
)

// NewRouter creates the HTTP router with all API routes and static file serving.
func NewRouter() http.Handler {
	handler, _ := NewRouterAndServer()
	return handler
}

// NewRouterAndServer builds the router and returns the Server alongside it, so the
// caller can flush in-flight migration state when the process shuts down.
func NewRouterAndServer() (http.Handler, *Server) {
	s := NewServer()
	staticFS, err := backend.StaticFS()
	if err != nil {
		log.Printf("Warning: could not load embedded frontend: %v", err)
	}
	return newRouterWithServer(s, staticFS), s
}

// newRouterWithServer creates the router with a given server (for testing).
func newRouterWithServer(s *Server, staticFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/v1/configure", s.HandleGetConfigure())
	mux.HandleFunc("POST /api/v1/configure", s.HandleConfigure())
	mux.HandleFunc("POST /api/v1/preflight", s.HandlePreflight())
	mux.HandleFunc("POST /api/v1/gather", s.HandleGather())
	mux.HandleFunc("GET /api/v1/gather", s.HandleGatherStatus())
	mux.HandleFunc("GET /api/v1/plan", s.HandlePlan())
	mux.HandleFunc("POST /api/v1/migrate", s.HandleMigrate())
	mux.HandleFunc("POST /api/v1/migrate/pause", s.HandleMigratePause())
	mux.HandleFunc("POST /api/v1/migrate/resume", s.HandleMigrateResume())
	mux.HandleFunc("GET /api/v1/status", s.HandleStatus())
	mux.HandleFunc("GET /api/v1/rate-limits", s.HandleRateLimits())
	mux.HandleFunc("GET /api/v1/checkpoint", s.HandleCheckpoint())
	mux.HandleFunc("GET /api/v1/scope", s.HandleGetScope())
	mux.HandleFunc("POST /api/v1/scope", s.HandleSetScope())
	mux.HandleFunc("POST /api/v1/scope/preview", s.HandlePreviewScope())
	mux.HandleFunc("POST /api/v1/run-options", s.HandleSetRunOptions())
	mux.HandleFunc("POST /api/v1/compare", s.HandleCompare())
	mux.HandleFunc("GET /api/v1/compare", s.HandleCompareStatus())
	mux.HandleFunc("POST /api/v1/converge", s.HandleConverge())
	mux.HandleFunc("POST /api/v1/cutover", s.HandleCutover())
	mux.HandleFunc("GET /api/v1/errors", s.HandleErrors())
	mux.HandleFunc("GET /api/v1/errors/export.csv", s.HandleErrorsExport())
	mux.HandleFunc("GET /api/v1/errors/{class}", s.HandleErrorClass())
	mux.HandleFunc("POST /api/v1/errors/{class}/resolve", s.HandleErrorClassResolve())
	mux.HandleFunc("GET /api/v1/report/service-tokens", s.HandleReportServiceTokens())
	mux.HandleFunc("GET /api/v1/tokens", s.HandleTokens())
	mux.HandleFunc("GET /api/v1/tokens/worklist.csv", s.HandleTokenWorklist())
	mux.HandleFunc("POST /api/v1/tokens/detect", s.HandleTokenDetect())
	mux.HandleFunc("POST /api/v1/tokens/issue", s.HandleTokenIssue())
	mux.HandleFunc("POST /api/v1/tokens/reroll", s.HandleTokenReroll())
	mux.HandleFunc("POST /api/v1/tokens/skip", s.HandleTokenSkip())
	mux.HandleFunc("POST /api/v1/tokens/skip-all", s.HandleTokenSkipAll())
	mux.HandleFunc("POST /api/v1/tokens/undo", s.HandleTokenUndo())
	mux.HandleFunc("POST /api/v1/tokens/defer", s.HandleTokenDefer())
	mux.HandleFunc("GET /api/v1/report/manual-steps", s.HandleReportManualSteps())
	mux.HandleFunc("GET /api/v1/report/summary", s.HandleReportSummary())
	mux.HandleFunc("GET /api/v1/standby", s.HandleGetStandby())
	mux.HandleFunc("POST /api/v1/standby", s.HandleSetStandby())
	mux.HandleFunc("POST /api/v1/standby/check", s.HandleStandbyCheck())
	mux.HandleFunc("GET /api/v1/report/final.html", s.HandleFinalReportHTML())
	mux.HandleFunc("GET /api/v1/report/final.json", s.HandleFinalReportJSON())

	// SSE endpoint
	mux.Handle("GET /api/v1/events", s.SSE)

	// Static files (frontend) — served for non-API routes
	if staticFS != nil {
		fileServer := http.FileServerFS(staticFS)
		mux.Handle("/", spaHandler(fileServer, staticFS))
	}

	// Apply middleware
	return chain(mux, requestIDMiddleware, loggingMiddleware, originGuardMiddleware)
}

// spaHandler serves static files, falling back to index.html for SPA routing.
func spaHandler(fileServer http.Handler, staticFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			path = "index.html"
		} else {
			// Strip leading slash
			path = path[1:]
		}

		// Never serve hidden files. Vite emits none, but the embed directory
		// carries a committed .gitkeep so //go:embed resolves on a clean
		// checkout, and that is not a public asset.
		if hasHiddenSegment(path) {
			http.NotFound(w, r)
			return
		}

		// Check if the file exists
		_, err := fs.Stat(staticFS, path)
		if err != nil {
			// File not found — serve index.html for SPA routing
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

// hasHiddenSegment reports whether any path segment begins with a dot.
func hasHiddenSegment(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

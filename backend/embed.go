package backend

import (
	"embed"
	"io/fs"
)

// staticFiles contains the built frontend assets.
//
// scripts/build.sh and the Dockerfile build the frontend with Vite and copy
// frontend/dist into backend/webui before compiling, which is what makes the
// binary self-contained. Everything in that directory is generated and
// git-ignored except .gitkeep.
//
// .gitkeep is committed for one mechanical reason: go:embed treats a pattern
// that matches no files as a compile error, so an absent or empty webui breaks
// `go build` and `go test ./...` on a clean checkout, before any frontend build
// has run. The bare `//go:embed webui` form cannot be used here — it skips
// names beginning with "." or "_", which would leave the directory with no
// embeddable files and fail the same way. Hence `all:`, which includes them.
//
// The build scripts clear webui but leave .gitkeep in place, so building never
// shows up as a source change. spaHandler refuses to serve dotfiles, so the
// keep-file is not reachable over HTTP.
//
//go:embed all:webui
var staticFiles embed.FS

// StaticFS returns the frontend filesystem rooted at webui.
func StaticFS() (fs.FS, error) {
	return fs.Sub(staticFiles, "webui")
}

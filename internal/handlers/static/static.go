// Package static serves vendored assets (JS, CSS) from disk under /static/.
// Conditional requests (ETag, Last-Modified) come free with ServeContent;
// add immutable caching only for content-hashed filenames.
package static

import (
	"net/http"
	"strings"

	"github.com/antoni-ostrowski/habit-tracker/internal/handlers"
)

// Register serves dir at GET /static/. Directory listings are 404;
// path traversal is rejected by the file server.
func Register(mux *http.ServeMux, dir string) {
	files := http.FileServer(http.Dir(dir))
	handlers.Route(mux, "GET /static/", http.StripPrefix("/static/", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/") {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
		})))
}

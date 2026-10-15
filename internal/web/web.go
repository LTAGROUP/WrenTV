// Package web serves the browser app: sign-in, claiming the server, browsing,
// playback and server management.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var files embed.FS

// Handler serves static files, falling back to index.html for app routes so
// deep links like /item/123 work.
func Handler() http.Handler {
	root, _ := fs.Sub(files, "static")
	fileServer := http.FileServer(http.FS(root))
	index, _ := fs.ReadFile(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(root, p); err == nil && !st.IsDir() {
				// Assets aren't fingerprinted, so revalidate each load.
				w.Header().Set("Cache-Control", "no-cache")
				fileServer.ServeHTTP(w, r)
				return
			}
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Frame-Options", "DENY")
		_, _ = w.Write(index)
	})
}

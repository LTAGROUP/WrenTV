// Package web serves the browser app: sign-in, claiming the server, browsing,
// playback and server management.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/config"
)

//go:embed static
var files embed.FS

func init() {
	// Not in Go's built-in table, and slim images ship no /etc/mime.types.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// contentPolicy limits what the page may run and load, so that text from
// outside (file and release names, channel and guide data, subtitles) that
// ever got onto the page unescaped still couldn't run as script or send the
// sign-in anywhere: scripts come only from this server, and requests go only
// to it and the account service. Artwork and channel logos may come from
// anywhere; styles are set inline throughout; hls.js runs a worker from a
// blob; trailers play in a YouTube frame.
var contentPolicy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data: blob: https: http:",
	"media-src 'self' blob: https: http:",
	"connect-src 'self' " + config.SupabaseURL,
	"frame-src https://www.youtube-nocookie.com",
	"worker-src 'self' blob:",
	"font-src 'self' data:",
	"manifest-src 'self'",
	"object-src 'none'",
	"base-uri 'self'",
	"form-action 'self'",
	"frame-ancestors 'none'",
}, "; ")

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
		w.Header().Set("Content-Security-Policy", contentPolicy)
		_, _ = w.Write(index)
	})
}

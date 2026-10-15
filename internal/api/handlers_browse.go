package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// handleBrowse lists sub-folders of a server path so admins can pick library folders.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = defaultBrowseRoot()
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		writeErr(w, 400, "path must be absolute")
		return
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, 400, "can't read folder: "+err.Error())
		return
	}
	type dir struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	dirs := []dir{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(p, e.Name())
		if !e.IsDir() {
			// Follow symlinks to folders.
			if st, err := os.Stat(full); err != nil || !st.IsDir() {
				continue
			}
		}
		dirs = append(dirs, dir{e.Name(), full})
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, 200, map[string]any{"path": p, "parent": parent, "dirs": dirs})
}

func defaultBrowseRoot() string {
	// Docker images mount media at /media by convention.
	if st, err := os.Stat("/media"); err == nil && st.IsDir() && runtime.GOOS == "linux" {
		return "/media"
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/"
}

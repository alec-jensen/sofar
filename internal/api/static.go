package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// static serves the built web app, falling back to index.html for client-side
// routes. Missing files with an extension are real 404s.
func (s *server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	root := s.Cfg.StaticDir
	path := filepath.Clean("/" + r.URL.Path)
	if strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/sw.js" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	full := filepath.Join(root, path)
	if info, err := os.Stat(full); err != nil || info.IsDir() {
		if filepath.Ext(path) != "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
		return
	}
	http.ServeFile(w, r, full)
}

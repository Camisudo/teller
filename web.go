package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// web/dist is the built React app. build.sh copies it in before compiling.
//
//go:embed all:web/dist
var webFS embed.FS

// spaHandler serves built frontend files and falls back to index.html so
// client-side routes (e.g. /deals) survive a browser refresh.
func (a *app) spaHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(sub, name); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}

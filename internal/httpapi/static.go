package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler 提供 SPA 静态资源；带 hash 的 assets 长缓存，其余 no-cache。
// 未命中的路径回退到 index.html（前端为 hash 路由，一般用不上，但保底）。
func (s *Server) staticHandler() http.Handler {
	if s.StaticFS == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not built; run `npm run build` in web/ or use the Docker image", http.StatusNotFound)
		})
	}
	fileServer := http.FileServerFS(s.StaticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}
		if _, err := fs.Stat(s.StaticFS, cleanPath); err != nil {
			s.serveIndex(w, r)
			return
		}
		switch {
		case strings.HasPrefix(cleanPath, "assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasSuffix(cleanPath, ".html"):
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.StaticFS, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

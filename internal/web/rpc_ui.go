package web

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// controlHandler serves one generated RPC surface and one embedded browser app.
func (c *Control) controlHandler() http.Handler {
	r := chi.NewRouter()
	r.Use(c.authorize)
	c.registerRPC(r)
	assets, _ := fs.Sub(Assets, "assets")
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for route, file := range map[string]string{"/openapi.json": "openapi.json", "/docs/node-openapi.json": "node-openapi.json", "/docs/broker-openapi.json": "broker-openapi.json", "/docs/api.md": "api.md", "/docs/cli.md": "cli.md", "/llms.txt": "llms.txt", "/llm.txt": "llms.txt"} {
		r.Get(route, c.documentation(file))
	}
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Funnel API reference</title></head><body><nav><a href="/">Control</a> · <a href="/docs/cli.md">CLI reference</a> · <a href="/docs/api.md">Agent API reference</a></nav><div id="reference"></div><script src="/assets/api/scalar.js"></script><script>Scalar.createApiReference('#reference',{url:'/openapi.json',hideClientButton:true,telemetry:false})</script></body></html>`))
	})
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			http.NotFound(w, r)
			return
		}
		b, e := Assets.ReadFile("assets/control/index.html")
		if e != nil {
			http.Error(w, "UI build unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
	return r
}
func (c *Control) documentation(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, e := Assets.ReadFile("assets/api/" + file)
		if e != nil {
			http.Error(w, "Generated documentation unavailable", 503)
			return
		}
		if file == "llms.txt" {
			b, e = c.agentDocumentation(b)
			if e != nil {
				http.Error(w, "Control documentation settings unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		contentType := "text/plain; charset=utf-8"
		if len(file) > 5 && file[len(file)-5:] == ".json" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
}

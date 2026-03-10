// Package web registers HTTP routes and serves the embedded frontend.
//
// Static assets (JS, CSS) and the HTML template are embedded into the
// binary using Go's embed package, eliminating the need to ship separate
// files alongside the executable.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"

	"syncwave/internal/ai"
	"syncwave/internal/hub"
)

//go:embed static
var staticFiles embed.FS

//go:embed templates/index.html
var indexHTML []byte

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, h *hub.Hub, assistant *ai.Assistant) {
	// Serve index.html at root
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	// Serve static files (JS, CSS) from embedded filesystem
	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// WebSocket endpoint
	mux.HandleFunc("/ws", h.ServeWS)

	// REST API
	mux.HandleFunc("/api/stats", h.ServeStats)
	mux.HandleFunc("/api/complete", handleComplete(assistant))
	mux.HandleFunc("/health", handleHealth(assistant))
}

// handleComplete streams AI text completions via Server-Sent Events.
func handleComplete(assistant *ai.Assistant) http.HandlerFunc {
	type completeRequest struct {
		TextBefore string `json:"textBefore"`
		TextAfter  string `json:"textAfter"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if assistant == nil {
			http.Error(w, `{"error":"AI not configured. Set GROQ_API_KEY"}`, http.StatusServiceUnavailable)
			return
		}

		var req completeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
			return
		}

		if len(req.TextBefore) < 10 {
			http.Error(w, `{"error":"Text too short (min 10 chars)"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, `{"error":"Streaming not supported"}`, http.StatusInternalServerError)
			return
		}

		textBefore := req.TextBefore
		if len(textBefore) > 500 {
			textBefore = textBefore[len(textBefore)-500:]
		}
		textAfter := req.TextAfter
		if len(textAfter) > 200 {
			textAfter = textAfter[:200]
		}

		stream := assistant.StreamComplete(r.Context(), textBefore, textAfter)

		for {
			select {
			case result, ok := <-stream:
				if !ok {
					fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				if result.Error != nil {
					data, _ := json.Marshal(map[string]string{"error": result.Error.Error()})
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
					return
				}
				data, _ := json.Marshal(map[string]string{"token": result.Token})
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

// handleHealth returns the server's health status.
func handleHealth(assistant *ai.Assistant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := "ready"
		if assistant == nil {
			status = "no_api_key"
		}
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}

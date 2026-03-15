package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"syncwave/internal/ai"
	"syncwave/internal/docs"
	"syncwave/internal/hub"
)

//go:embed static
var staticFiles embed.FS

//go:embed templates/index.html
var indexHTML []byte

//go:embed templates/home.html
var homeHTML []byte

type completer interface {
	StreamComplete(ctx context.Context, textBefore, textAfter string) <-chan ai.StreamResult
}

type documentService interface {
	CreateDocument(title string) (docs.Doc, error)
	ListDocuments(limit int) ([]docs.Doc, error)
}

func RegisterRoutes(mux *http.ServeMux, h *hub.Hub, assistant *ai.Assistant, docService documentService) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(homeHTML)
	})

	mux.HandleFunc("/editor", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		_, _ = w.Write(indexHTML)
	})

	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/docs", handleDocs(docService))

	mux.HandleFunc("/ws", h.ServeWS)
	var c completer
	if assistant != nil {
		c = assistant
	}
	mux.HandleFunc("/api/complete", handleComplete(c))
}

func handleDocs(docService documentService) http.HandlerFunc {
	type createDocRequest struct {
		Title string `json:"title"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if docService == nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"Document service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		switch r.Method {
		case http.MethodGet:
			limit := 50
			if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
				if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
					limit = n
				}
			}

			docs, err := docService.ListDocuments(limit)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"Failed to list documents"}`, http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"documents": docs})
			return

		case http.MethodPost:
			var req createDocRequest
			_ = json.NewDecoder(r.Body).Decode(&req)

			doc, err := docService.CreateDocument(req.Title)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"Failed to create document"}`, http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"document": doc,
				"url":      "/editor?doc_id=" + doc.ID,
			})
			return

		default:
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
	}
}

func handleComplete(assistant completer) http.HandlerFunc {
	type completeRequest struct {
		TextBefore string `json:"textBefore"`
		TextAfter  string `json:"textAfter"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if assistant == nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"AI not configured. Set GROQ_API_KEY"}`, http.StatusServiceUnavailable)
			return
		}

		var req completeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if len(req.TextBefore) < 10 {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"Text too short (min 10 chars)"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"Streaming not supported"}`, http.StatusInternalServerError)
			return
		}

		textBefore := req.TextBefore
		if len(textBefore) > 2000 {
			textBefore = textBefore[len(textBefore)-2000:]
		}
		textAfter := req.TextAfter
		if len(textAfter) > 900 {
			textAfter = textAfter[:900]
		}

		stream := assistant.StreamComplete(r.Context(), textBefore, textAfter)
		for {
			select {
			case result, ok := <-stream:
				if !ok {
					if err := writeSSE(w, flusher, "[DONE]"); err != nil {
						return
					}
					return
				}
				if result.Error != nil {
					data, _ := json.Marshal(map[string]string{"error": result.Error.Error()})
					if err := writeSSE(w, flusher, string(data)); err != nil {
						return
					}
					return
				}
				data, _ := json.Marshal(map[string]string{"token": result.Token})
				if err := writeSSE(w, flusher, string(data)); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, data string) error {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

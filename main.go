package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"syncwave/ai"
	"syncwave/network"

	"github.com/joho/godotenv"
)

type CompleteRequest struct {
	TextBefore string `json:"textBefore"`
	TextAfter  string `json:"textAfter"`
}

var assistant *ai.Assistant

func main() {
	godotenv.Load(".env")

	// Initialize AI
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		log.Println("Warning: GROQ_API_KEY not set, AI features disabled")
	} else {
		var err error
		assistant, err = ai.NewAssistant(apiKey)
		if err != nil {
			log.Printf("Failed to initialize AI assistant: %v", err)
		} else {
			log.Println("AI Assistant initialized (Groq: llama-3.1-8b-instant)")
		}
	}

	// Initialize Hub
	hub := network.NewHub()

	// Routes
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)
	http.HandleFunc("/ws", hub.ServeWS)
	http.HandleFunc("/api/stats", hub.ServeStats)
	http.HandleFunc("/api/complete", handleComplete)
	http.HandleFunc("/health", handleHealth)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("╔═══════════════════════════════════════════════════╗")
	fmt.Println("║   SyncWave: Distributed Collaboration Engine      ║")
	fmt.Println("║   CRDT + AI-Powered Real-Time Document Editing    ║")
	fmt.Println("╠═══════════════════════════════════════════════════╣")
	fmt.Printf("║  Open http://localhost:%s in multiple tabs       ║\n", port)
	fmt.Println("╚═══════════════════════════════════════════════════╝")

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Println("Server error:", err)
	}
}

func handleComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if assistant == nil {
		http.Error(w, `{"error": "AI not configured. Set GROQ_API_KEY"}`, http.StatusServiceUnavailable)
		return
	}

	var req CompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if len(req.TextBefore) < 10 {
		http.Error(w, `{"error": "Text too short (min 10 chars)"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error": "Streaming not supported"}`, http.StatusInternalServerError)
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

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "ready"
	if assistant == nil {
		status = "no_api_key"
	}
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

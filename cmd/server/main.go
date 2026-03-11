// SyncWave — A distributed collaborative text editor built with Go.
//
// This is the application entry point. It loads configuration from
// environment variables, initializes the AI assistant, collaboration hub,
// and HTTP server with middleware, then starts listening for connections
// with graceful shutdown support.
//
// Usage:
//
//	PORT=8080 GROQ_API_KEY=your_key go run ./cmd/server
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syncwave/internal/ai"
	"syncwave/internal/config"
	"syncwave/internal/hub"
	"syncwave/internal/middleware"
	"syncwave/internal/web"
)

func main() {
	// ── Load Configuration ──────────────────────────────────────────
	cfg := config.Load()

	// ── Structured Logger ───────────────────────────────────────────
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	// ── AI Assistant (optional) ─────────────────────────────────────
	var assistant *ai.Assistant
	if cfg.GroqAPIKey != "" {
		var err error
		assistant, err = ai.NewAssistant(cfg.GroqAPIKey)
		if err != nil {
			logger.Error("failed to initialize AI assistant", "error", err)
		} else {
			logger.Info("AI assistant initialized", "model", "llama-3.1-8b-instant")
		}
	} else {
		logger.Warn("GROQ_API_KEY not set — AI features disabled")
	}

	// ── Collaboration Hub ───────────────────────────────────────────
	h := hub.NewHub(logger)

	// ── HTTP Router & Middleware ─────────────────────────────────────
	mux := http.NewServeMux()
	web.RegisterRoutes(mux, h, assistant)

	handler := middleware.Chain(mux,
		middleware.Recovery(logger),
		middleware.RequestLogger(logger),
		middleware.CORS(),
	)

	// ── HTTP Server ─────────────────────────────────────────────────
	srv := &http.Server{
		Addr:        ":" + cfg.Port,
		Handler:     handler,
		ReadTimeout: 15 * time.Second,
		IdleTimeout: 120 * time.Second,
		// WriteTimeout deliberately omitted: SSE streams require open-ended
		// writes, and WebSocket connections are hijacked by gorilla/websocket.
	}

	// ── Graceful Shutdown ───────────────────────────────────────────
	// Listen for SIGINT (Ctrl+C) or SIGTERM (Docker/Render) to shut
	// down gracefully, giving active connections time to finish.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("SyncWave server starting",
			"port", cfg.Port,
			"ai", cfg.GroqAPIKey != "",
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	logger.Info("shutdown signal received, draining connections...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h.Shutdown()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	logger.Info("server stopped")
}

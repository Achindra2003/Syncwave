package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syncwave/internal/ai"
	"syncwave/internal/analytics"
	"syncwave/internal/config"
	"syncwave/internal/docs"
	"syncwave/internal/hub"
	"syncwave/internal/web"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	var assistant *ai.Assistant
	if cfg.GroqAPIKey != "" {
		var err error
		assistant, err = ai.NewAssistant(cfg.GroqAPIKey)
		if err != nil {
			logger.Error("failed to initialize AI assistant", "error", err)
		}
	} else {
		logger.Warn("GROQ_API_KEY not set — AI features disabled")
	}

	h := hub.NewHub(logger)
	h.ConfigureAllowedOrigins(cfg.AllowedOrigins)

	docService, err := docs.NewPostgresService(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to initialize Postgres service", "error", err)
		os.Exit(1)
	}

	// Initialize Advanced Features: Analytics Worker Pool
	pool := analytics.NewWorkerPool(100, h)
	pool.Start(3) // 3 background workers
	defer pool.Stop()

	// Make the hub aware of the background pool so document actions can trigger analytics
	h.SetPersistence(
		func(docID string) (string, int, error) {
			return docService.LoadStateOrCreate(docID)
		},
		func(docID string, content string, seq int) error {
			// Trigger analytics job asynchronously
			pool.Submit(docID, content)
			return docService.SaveState(docID, content, seq)
		},
	)

	mux := http.NewServeMux()
	web.RegisterRoutes(mux, h, assistant, docService)

	srv := &http.Server{
		Addr:        fmt.Sprintf(":%s", cfg.Port),
		Handler:     mux,
		IdleTimeout: 120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("SyncWave server starting", "port", cfg.Port, "ai", assistant != nil)
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

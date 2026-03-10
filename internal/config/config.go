// Package config centralizes application configuration management.
// It loads values from environment variables, following the twelve-factor
// app methodology. A .env file is supported for local development.
package config

import (
	"log/slog"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all application configuration values.
type Config struct {
	Port       string     // HTTP server port (default: "8080")
	GroqAPIKey string     // API key for Groq LLM service
	LogLevel   slog.Level // Logging verbosity (info or debug)
}

// Load reads configuration from environment variables.
// It attempts to load a .env file first for local development;
// the file is silently ignored in production if absent.
func Load() *Config {
	_ = godotenv.Load(".env")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}

	return &Config{
		Port:       port,
		GroqAPIKey: os.Getenv("GROQ_API_KEY"),
		LogLevel:   level,
	}
}

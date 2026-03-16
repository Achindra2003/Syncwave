// Package config centralizes application configuration management.
// It loads values from environment variables, following the twelve-factor
// app methodology. A .env file is supported for local development.
package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration values.
type Config struct {
	Port           string            // HTTP server port (default: "8080")
	DatabaseURL    string            // PostgreSQL DSN (required for persistent storage)
	GroqAPIKey     string            // API key for Groq LLM service
	LogLevel       slog.Level        // Logging verbosity (info or debug)
	SnapshotEvery  int               // Persist snapshot every N ops (default: 25)
	AuthSecret     string            // Secret for signed websocket sessions (optional)
	SessionTTLMin  int               // Session token TTL in minutes (default: 480)
	RoomKeys       map[string]string // Optional per-room access keys
	AllowedOrigins []string          // Allowed CORS/WS origins (default: allow all)
	RedisURL       string            // Redis URL for cross-instance pubsub (optional)
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

	snapshotEvery := 25
	if raw := os.Getenv("SNAPSHOT_EVERY"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			snapshotEvery = n
		}
	}

	sessionTTLMin := 480
	if raw := os.Getenv("SESSION_TTL_MIN"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			sessionTTLMin = n
		}
	}

	roomKeys := map[string]string{}
	if raw := os.Getenv("ROOM_KEYS_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &roomKeys)
	}

	allowedOrigins := []string{}
	if raw := os.Getenv("ALLOWED_ORIGINS"); raw != "" {
		parts := strings.Split(raw, ",")
		for _, p := range parts {
			origin := strings.TrimSpace(p)
			if origin != "" {
				allowedOrigins = append(allowedOrigins, origin)
			}
		}
	}

	return &Config{
		Port:           port,
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		GroqAPIKey:     os.Getenv("GROQ_API_KEY"),
		LogLevel:       level,
		SnapshotEvery:  snapshotEvery,
		AuthSecret:     os.Getenv("AUTH_SECRET"),
		SessionTTLMin:  sessionTTLMin,
		RoomKeys:       roomKeys,
		AllowedOrigins: allowedOrigins,
		RedisURL:       os.Getenv("REDIS_URL"),
	}
}

func getOrDefault(key string, fallback string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

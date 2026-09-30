package config

import "os"

type Config struct {
	HTTPAddr     string
	DatabasePath string
}

func Load() Config {
	return Config{
		HTTPAddr:     envOrDefault("HTTP_ADDR", ":8083"),
		DatabasePath: envOrDefault("DATABASE_PATH", "data/gomoku.db"),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

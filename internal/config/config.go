// Package config reads process configuration from the environment.
package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	Addr        string
	DatabaseURL string
	Env         string
}

// Load reads .env (if present) into the environment, then builds a Config.
// Real environment variables always win over .env, so production deploys can
// set them without a file.
func Load() Config {
	loadDotEnv(".env")

	return Config{
		Addr:        env("ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", ""),
		Env:         env("ENV", "dev"),
	}
}

func (c Config) IsDev() bool { return c.Env == "dev" }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv parses KEY=value lines. Missing file is not an error.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

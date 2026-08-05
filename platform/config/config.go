// Package config is the ONLY place viper lives. It reads environment variables
// and produces a plain Config struct. Nothing else in the codebase calls
// viper.Get(): main, api, store, and engine receive plain values as parameters.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	DBDSN       string
	APIKey      string
	HTTPPort    int
	LogLevel    string
	DisableDocs bool
}

// Setting names, read from the environment or from a local .env file. They are
// deliberately unprefixed — the same spelling works in the shell, in .env, and in
// docker-compose.yml.
//
// DB_DSN and API_KEY have no defaults and are required: the application fails fast
// if either is unset, rather than silently connecting to the wrong database or
// starting up unauthenticated.
const (
	keyDBDSN       = "DB_DSN"
	keyAPIKey      = "API_KEY"
	keyHTTPPort    = "HTTP_PORT"
	keyLogLevel    = "LOG_LEVEL"
	keyDisableDocs = "DISABLE_DOCS"
)

func Load() (Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault(keyHTTPPort, 8080)
	v.SetDefault(keyLogLevel, "info")
	v.SetDefault(keyDisableDocs, false)

	// Best-effort local .env; absence is not an error. The file lives at the
	// repo root, so search upward from the working directory to find it. This
	// makes `go run` work whether invoked from the root or a subdirectory such
	// as cmd/migrate.
	v.SetConfigType("env")
	if envPath := FindUpwards(".env"); envPath != "" {
		v.SetConfigFile(envPath)
		_ = v.ReadInConfig()
	}

	dsn := v.GetString(keyDBDSN)
	if dsn == "" {
		return Config{}, fmt.Errorf("config: %s is required (set it in the environment or .env)", keyDBDSN)
	}

	apiKey := v.GetString(keyAPIKey)
	if apiKey == "" {
		return Config{}, fmt.Errorf("config: %s is required (set it in the environment or .env)", keyAPIKey)
	}

	return Config{
		DBDSN:       dsn,
		APIKey:      apiKey,
		HTTPPort:    v.GetInt(keyHTTPPort),
		LogLevel:    v.GetString(keyLogLevel),
		DisableDocs: v.GetBool(keyDisableDocs),
	}, nil
}

// Walks from the working directory toward the filesystem root and returns the
// path of the first directory that contains name, or "" if none.
//
// Used to locate repo-root paths (.env, migrations/) regardless of where the
// binary was launched from — which is what lets `go run ./cmd/migrate` work from
// a subdirectory.
func FindUpwards(name string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root
		}
		dir = parent
	}
}

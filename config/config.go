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

// Load reads configuration from the environment (prefix JAST_). It optionally
// reads a .env file for local development. JAST_DB_DSN has no default and is
// required: the application fails fast if it is unset, rather than silently
// connecting to the wrong database.
func Load() (Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("HTTP_PORT", 8080)
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("DISABLE_DOCS", true)

	// Best-effort local .env; absence is not an error. The file lives at the
	// repo root, so search upward from the working directory to find it. This
	// makes `go run` work whether invoked from the root or a subdirectory such
	// as cmd/migrate.
	v.SetConfigType("env")
	if envPath := findUpwards(".env"); envPath != "" {
		v.SetConfigFile(envPath)
		_ = v.ReadInConfig()
	}

	dsn := v.GetString("DB_DSN")
	if dsn == "" {
		return Config{}, fmt.Errorf("config: DB_DSN is required (set it in the environment or .env)")
	}

	apiKey := v.GetString("API_KEY")
	if apiKey == "" {
		return Config{}, fmt.Errorf("config: API_KEY is required (set it in the environment or .env)")
	}

	return Config{
		DBDSN:       dsn,
		APIKey:      apiKey,
		HTTPPort:    v.GetInt("HTTP_PORT"),
		LogLevel:    v.GetString("LOG_LEVEL"),
		DisableDocs: v.GetBool("DISABLE_DOCS"),
	}, nil
}

// findUpwards walks from the working directory toward the filesystem root and
// returns the path of the first directory that contains name, or "" if none.
// Used to locate repo-root files (.env, migrations/) regardless of where the
// binary was launched from.
func findUpwards(name string) string {
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

// FindUpwards is the exported form of findUpwards, for other packages (e.g. the
// migrate command locating the migrations/ directory) that need the same
// working-directory-independent lookup.
func FindUpwards(name string) string {
	return findUpwards(name)
}

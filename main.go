package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"bekasi-automatic-trading-system/api"
	"bekasi-automatic-trading-system/config"
	"bekasi-automatic-trading-system/store"
)

func main() {
	if err := runServer(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func runServer() error {
	cfg, err := config.Load()
	if err != nil {
		return err // fails fast when JAST_DB_DSN is unset
	}

	setupLogger(cfg.LogLevel)

	ctx := context.Background()
	st, err := store.New(ctx, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer st.Close()

	srv, err := api.NewServer(ctx, st)
	if err != nil {
		return err
	}

	addr := ":" + strconv.Itoa(cfg.HTTPPort)
	slog.Info("starting JAST core", "addr", addr)
	return http.ListenAndServe(addr, srv.Handler())
}

func setupLogger(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}

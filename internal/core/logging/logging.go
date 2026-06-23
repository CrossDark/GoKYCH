package logging

import (
	"log/slog"
	"os"
)

// Init configures the default slog logger. In debug mode the level is set to
// Debug so verbose startup / per-request diagnostics are visible; in release
// mode it stays at Info.
func Init(ginMode string) {
	level := slog.LevelInfo
	if ginMode == "debug" {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}

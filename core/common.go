package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// GetConfig extracts *config.Config from context
func GetConfig(ctx context.Context) (*Config, error) {
	cfg, ok := ctx.Value("config").(*Config)
	if !ok {
		return nil, fmt.Errorf("invalid config type")
	}
	return cfg, nil
}

// SetupLogging sets slog default logger to the given level
func SetupLogging(level slog.Level) {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

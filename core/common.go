package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextKey is a custom type for context keys to avoid collisions
type ContextKey string

const (
	// ConfigKey is the context key for storing Config
	ConfigKey ContextKey = "config"
)

// GetConfig extracts *config.Config from context
func GetConfig(ctx context.Context) (*Config, error) {
	cfg, ok := ctx.Value(ConfigKey).(*Config)
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

// DiscoverRouters scans the current directory for router*.rsc files
// and returns a sorted list of router names (without .rsc extension, with .home appended)
func DiscoverRouters() ([]string, error) {
	files, err := filepath.Glob("router*.rsc")
	if err != nil {
		return nil, fmt.Errorf("failed to scan for router files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no router*.rsc files found in current directory")
	}

	// Sort files naturally
	sort.Strings(files)

	var routers []string
	for _, file := range files {
		// Remove .rsc extension and add .home
		basename := strings.TrimSuffix(file, ".rsc")
		routers = append(routers, basename+".home")
	}

	return routers, nil
}

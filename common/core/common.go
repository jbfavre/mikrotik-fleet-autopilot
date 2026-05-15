package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ContextKey is a custom type for context keys to avoid collisions
type ContextKey string

const (
	// ConfigKey is the context key for storing Config
	ConfigKey ContextKey = "config"
	// SshManagerKey is the context key for storing SshManager
	SshManagerKey ContextKey = "ssh_manager"
	// EnrollmentKey is the context key for storing enrollment mode
	EnrollmentKey ContextKey = "enrollment"
	// HostKeyAliasKey is the context key for storing the device identity used for host key lookups.
	HostKeyAliasKey ContextKey = "host_key_alias"
)

type slogRouterWriter struct {
	mu       sync.RWMutex
	fallback io.Writer
	live     io.Writer
}

func (w *slogRouterWriter) Write(p []byte) (int, error) {
	w.mu.RLock()
	live := w.live
	fallback := w.fallback
	w.mu.RUnlock()
	if live != nil {
		return live.Write(p)
	}
	return fallback.Write(p)
}

func (w *slogRouterWriter) SetLiveWriter(writer io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.live = writer
}

var defaultSlogWriter = &slogRouterWriter{fallback: os.Stderr}

// GetConfig extracts *config.Config from context
func GetConfig(ctx context.Context) (*Config, error) {
	cfg, ok := ctx.Value(ConfigKey).(*Config)
	if !ok {
		return nil, fmt.Errorf("invalid config type")
	}
	return cfg, nil
}

// GetSshManager extracts SshManager from context and returns it as a generic value.
// Caller must type assert to the appropriate ssh.SshManager type
func GetSshManager(ctx context.Context) (any, error) {
	manager := ctx.Value(SshManagerKey)
	if manager == nil {
		return nil, fmt.Errorf("ssh manager not found in context")
	}
	return manager, nil
}

// IsEnrollmentMode checks if the context is in enrollment mode
func IsEnrollmentMode(ctx context.Context) bool {
	mode, ok := ctx.Value(EnrollmentKey).(bool)
	return ok && mode
}

// SetupLogging sets slog default logger to the given level
func SetupLogging(level slog.Level) {
	handler := slog.NewTextHandler(defaultSlogWriter, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// SetLiveLogWriter routes slog output to writer while live display is active.
// Pass nil to disable live routing and fall back to stderr.
func SetLiveLogWriter(writer io.Writer) {
	defaultSlogWriter.SetLiveWriter(writer)
}

// ParseHosts parses a comma-separated list of hosts and returns a slice of trimmed host strings.
// Empty strings and whitespace-only entries are filtered out.
func ParseHosts(hosts string) []string {
	if hosts == "" {
		return []string{}
	}

	hostsList := []string{}
	for h := range strings.SplitSeq(hosts, ",") {
		trimmed := strings.TrimSpace(h)
		if trimmed != "" {
			hostsList = append(hostsList, trimmed)
		}
	}
	return hostsList
}

// DiscoverHosts scans the current directory for router*.rsc files
// and returns a sorted list of router names (without .rsc extension, with .home appended)
func DiscoverHosts() ([]string, error) {
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
		// Remove .rsc extension (let ssh_config handle domain expansion)
		basename := strings.TrimSuffix(file, ".rsc")
		routers = append(routers, basename)
	}

	return routers, nil
}

package core

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGetConfig(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		wantErr   bool
		wantHosts []string
	}{
		{
			name: "valid config in context",
			ctx: context.WithValue(context.Background(), ConfigKey, &Config{
				Hosts: []string{"router1.home", "router2.home"},
				User:  "admin",
				Debug: true,
			}),
			wantErr:   false,
			wantHosts: []string{"router1.home", "router2.home"},
		},
		{
			name:    "no config in context",
			ctx:     context.Background(),
			wantErr: true,
		},
		{
			name:    "wrong type in context",
			ctx:     context.WithValue(context.Background(), ConfigKey, "not a config"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := GetConfig(tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(cfg.Hosts, tt.wantHosts) {
				t.Errorf("GetConfig() hosts = %v, want %v", cfg.Hosts, tt.wantHosts)
			}
		})
	}
}

func TestDiscoverHosts(t *testing.T) {
	tests := []struct {
		name        string
		setupFiles  []string
		wantRouters []string
		wantErr     bool
	}{
		{
			name:        "single router file",
			setupFiles:  []string{"router1.rsc"},
			wantRouters: []string{"router1.home"},
			wantErr:     false,
		},
		{
			name:        "multiple router files",
			setupFiles:  []string{"router1.rsc", "router2.rsc", "router3.rsc"},
			wantRouters: []string{"router1.home", "router2.home", "router3.home"},
			wantErr:     false,
		},
		{
			name:        "router files with different prefixes (sorted)",
			setupFiles:  []string{"router10.rsc", "router2.rsc", "router1.rsc"},
			wantRouters: []string{"router1.home", "router10.home", "router2.home"},
			wantErr:     false,
		},
		{
			name:       "no router files",
			setupFiles: []string{},
			wantErr:    true,
		},
		{
			name:       "non-router files only",
			setupFiles: []string{"config.txt", "backup.rsc"},
			wantErr:    true,
		},
		{
			name:        "mixed files",
			setupFiles:  []string{"router-main.rsc", "config.txt", "router-backup.rsc"},
			wantRouters: []string{"router-backup.home", "router-main.home"},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "router-test-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Save current directory and change to temp directory
			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get current dir: %v", err)
			}
			defer func() { _ = os.Chdir(originalDir) }()

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("failed to change to temp dir: %v", err)
			}

			// Create test files
			for _, filename := range tt.setupFiles {
				if err := os.WriteFile(filename, []byte("test content"), 0644); err != nil {
					t.Fatalf("failed to create test file %s: %v", filename, err)
				}
			}

			// Run the test
			routers, err := DiscoverHosts()

			if (err != nil) != tt.wantErr {
				t.Errorf("DiscoverHosts() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(routers, tt.wantRouters) {
				t.Errorf("DiscoverHosts() = %v, want %v", routers, tt.wantRouters)
			}
		})
	}
}

func TestDiscoverHostsRealDirectory(t *testing.T) {
	// This test checks behavior with actual file system operations
	tmpDir, err := os.MkdirTemp("", "router-discover-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create some router files
	files := []string{"router1.rsc", "router2.rsc"}
	for _, file := range files {
		path := filepath.Join(tmpDir, file)
		if err := os.WriteFile(path, []byte("# RouterOS config\n"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	// Change to temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change dir: %v", err)
	}

	routers, err := DiscoverHosts()
	if err != nil {
		t.Errorf("DiscoverHosts() unexpected error: %v", err)
	}

	expected := []string{"router1.home", "router2.home"}
	if !reflect.DeepEqual(routers, expected) {
		t.Errorf("DiscoverHosts() = %v, want %v", routers, expected)
	}
}

// mockError implements the error interface for testing
type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}

func TestContextKey(t *testing.T) {
	// Test that ConfigKey is properly typed
	key := ConfigKey
	if key != "config" {
		t.Errorf("ConfigKey = %q, want %q", key, "config")
	}

	// Test that different ContextKey values are distinguishable
	otherKey := ContextKey("other")
	if key == otherKey {
		t.Error("ConfigKey should not equal other ContextKey values")
	}
}

func BenchmarkDiscoverHosts(b *testing.B) {
	// Create temporary directory with test files
	tmpDir, err := os.MkdirTemp("", "router-bench-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create 10 router files
	for i := 1; i <= 10; i++ {
		filename := filepath.Join(tmpDir, "router"+string(rune(i+'0'))+".rsc")
		if err := os.WriteFile(filename, []byte("# config\n"), 0644); err != nil {
			b.Fatalf("failed to create file: %v", err)
		}
	}

	originalDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalDir) }()
	_ = os.Chdir(tmpDir)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DiscoverHosts()
	}
}

func BenchmarkGetConfig(b *testing.B) {
	ctx := context.WithValue(context.Background(), ConfigKey, &Config{
		Hosts: []string{"router1.home", "router2.home"},
		User:  "admin",
		Debug: false,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetConfig(ctx)
	}
}

func TestParseHosts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single host",
			input:    "192.168.1.1",
			expected: []string{"192.168.1.1"},
		},
		{
			name:     "multiple hosts with comma",
			input:    "192.168.1.1,192.168.1.2",
			expected: []string{"192.168.1.1", "192.168.1.2"},
		},
		{
			name:     "multiple hosts with spaces",
			input:    "192.168.1.1, 192.168.1.2, 192.168.1.3",
			expected: []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"},
		},
		{
			name:     "hosts with trailing comma",
			input:    "192.168.1.1,192.168.1.2,",
			expected: []string{"192.168.1.1", "192.168.1.2"},
		},
		{
			name:     "hosts with leading spaces",
			input:    " 192.168.1.1,  192.168.1.2",
			expected: []string{"192.168.1.1", "192.168.1.2"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "only spaces and commas",
			input:    " , , ",
			expected: []string{},
		},
		{
			name:     "hostname instead of IP",
			input:    "router.example.com",
			expected: []string{"router.example.com"},
		},
		{
			name:     "mixed hostnames and IPs",
			input:    "router1.local,192.168.1.1,router2.local",
			expected: []string{"router1.local", "192.168.1.1", "router2.local"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseHosts(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ParseHosts(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseHostsPerformance(t *testing.T) {
	// Test with a large number of hosts
	largeInput := strings.Repeat("192.168.1.1,", 1000)
	result := ParseHosts(largeInput)

	if len(result) != 1000 {
		t.Errorf("Expected 1000 hosts, got %d", len(result))
	}
}

func BenchmarkParseHosts(b *testing.B) {
	input := "192.168.1.1,192.168.1.2,192.168.1.3,router.local"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseHosts(input)
	}
}

func BenchmarkParseHostsLarge(b *testing.B) {
	// Benchmark with 100 hosts
	input := strings.Repeat("192.168.1.1,", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseHosts(input)
	}
}
func TestSetupLogging(t *testing.T) {
	// Test different log levels
	tests := []struct {
		name  string
		level int
	}{
		{
			name:  "debug level",
			level: -4, // slog.LevelDebug
		},
		{
			name:  "info level",
			level: 0, // slog.LevelInfo
		},
		{
			name:  "warn level",
			level: 4, // slog.LevelWarn
		},
		{
			name:  "error level",
			level: 8, // slog.LevelError
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// SetupLogging should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("SetupLogging() panicked: %v", r)
				}
			}()

			// Call SetupLogging with the test level
			SetupLogging(slog.Level(tt.level))

			// Verify that slog is configured by attempting to log
			slog.Debug("test debug message")
			slog.Info("test info message")
		})
	}
}

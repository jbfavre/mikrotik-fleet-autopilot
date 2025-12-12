package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
				Hosts:    []string{"router1.home", "router2.home"},
				User:     "admin",
				Password: "secret",
				Debug:    true,
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

func TestDiscoverRouters(t *testing.T) {
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
			routers, err := DiscoverRouters()

			if (err != nil) != tt.wantErr {
				t.Errorf("DiscoverRouters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(routers, tt.wantRouters) {
				t.Errorf("DiscoverRouters() = %v, want %v", routers, tt.wantRouters)
			}
		})
	}
}

func TestDiscoverRoutersRealDirectory(t *testing.T) {
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

	routers, err := DiscoverRouters()
	if err != nil {
		t.Errorf("DiscoverRouters() unexpected error: %v", err)
	}

	expected := []string{"router1.home", "router2.home"}
	if !reflect.DeepEqual(routers, expected) {
		t.Errorf("DiscoverRouters() = %v, want %v", routers, expected)
	}
}

func TestIsAlreadyClosedError(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantRes bool
	}{
		{
			name:    "nil error",
			errMsg:  "",
			wantRes: false,
		},
		{
			name:    "use of closed network connection",
			errMsg:  "use of closed network connection",
			wantRes: true,
		},
		{
			name:    "connection already closed",
			errMsg:  "connection already closed",
			wantRes: true,
		},
		{
			name:    "partial match - closed network",
			errMsg:  "error: use of closed network connection detected",
			wantRes: true,
		},
		{
			name:    "partial match - already closed",
			errMsg:  "ssh: connection already closed by remote",
			wantRes: true,
		},
		{
			name:    "different error",
			errMsg:  "connection timeout",
			wantRes: false,
		},
		{
			name:    "authentication failed",
			errMsg:  "ssh: unable to authenticate",
			wantRes: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &mockError{msg: tt.errMsg}
			}

			result := IsAlreadyClosedError(err)
			if result != tt.wantRes {
				t.Errorf("IsAlreadyClosedError(%q) = %v, want %v", tt.errMsg, result, tt.wantRes)
			}
		})
	}
}

// mockError implements the error interface for testing
type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}

func TestConfigStruct(t *testing.T) {
	// Test Config struct initialization and field access
	cfg := Config{
		Hosts:    []string{"router1.home", "router2.home"},
		User:     "admin",
		Password: "password123",
		Debug:    true,
	}

	if len(cfg.Hosts) != 2 {
		t.Errorf("Config.Hosts length = %d, want 2", len(cfg.Hosts))
	}

	if cfg.User != "admin" {
		t.Errorf("Config.User = %s, want admin", cfg.User)
	}

	if cfg.Password != "password123" {
		t.Errorf("Config.Password = %s, want password123", cfg.Password)
	}

	if !cfg.Debug {
		t.Error("Config.Debug = false, want true")
	}
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

func BenchmarkDiscoverRouters(b *testing.B) {
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
		_, _ = DiscoverRouters()
	}
}

func BenchmarkGetConfig(b *testing.B) {
	ctx := context.WithValue(context.Background(), ConfigKey, &Config{
		Hosts:    []string{"router1.home", "router2.home"},
		User:     "admin",
		Password: "secret",
		Debug:    false,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetConfig(ctx)
	}
}

func BenchmarkIsAlreadyClosedError(b *testing.B) {
	err := &mockError{msg: "use of closed network connection"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsAlreadyClosedError(err)
	}
}

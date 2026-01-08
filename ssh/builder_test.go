package ssh

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Mock implementations for testing

type mockConfigReader struct {
	hostInfo *HostInfo
	err      error
}

func (m *mockConfigReader) ReadConfig(host string) (*HostInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.hostInfo != nil {
		return m.hostInfo, nil
	}
	return &HostInfo{
		Hostname: host,
		Port:     "22",
		User:     "admin",
	}, nil
}

func TestNewConnectionBuilder(t *testing.T) {
	configReader := &mockConfigReader{}

	builder := NewConnectionBuilder(configReader)

	if builder == nil {
		t.Fatal("NewConnectionBuilder() returned nil")
	}

	if builder.configReader != configReader {
		t.Error("NewConnectionBuilder() did not set configReader correctly")
	}
}

func TestDefaultConnectionBuilder_Build_NoAuth(t *testing.T) {
	configReader := &mockConfigReader{}

	builder := NewConnectionBuilder(configReader)
	ctx := context.Background()

	_, err := builder.Build(ctx, "test.host:22", "admin", "", "", nil)
	if err == nil {
		t.Error("Build() expected error for no authentication, got nil")
	}

	if !strings.Contains(err.Error(), "failed to build auth methods") {
		t.Errorf("Build() error = %q, want error containing 'failed to build auth methods'", err.Error())
	}
}

func TestDefaultConnectionBuilder_Build_ContextCanceled(t *testing.T) {
	configReader := &mockConfigReader{}

	builder := NewConnectionBuilder(configReader)

	// Create a context that's already canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := builder.Build(ctx, "nonexistent.host:22", "admin", "password", "", nil)
	if err == nil {
		t.Error("Build() expected error for canceled context, got nil")
	}
}

func TestDefaultConnectionBuilder_Build_Timeout(t *testing.T) {
	configReader := &mockConfigReader{}

	builder := NewConnectionBuilder(configReader)

	// Create a context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give it time to expire
	time.Sleep(10 * time.Millisecond)

	_, err := builder.Build(ctx, "nonexistent.host:22", "admin", "password", "", nil)
	if err == nil {
		t.Error("Build() expected error for timeout, got nil")
	}
}

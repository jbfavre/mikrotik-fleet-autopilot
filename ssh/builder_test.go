package ssh

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

type mockAuthProvider struct {
	methods []ssh.AuthMethod
	err     error
}

func (m *mockAuthProvider) BuildAuthMethods(hostInfo *HostInfo, password, passphrase string) ([]ssh.AuthMethod, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.methods, nil
}

func TestNewConnectionBuilder(t *testing.T) {
	configReader := &mockConfigReader{}
	authProvider := &mockAuthProvider{}

	builder := NewConnectionBuilder(configReader, authProvider)

	if builder == nil {
		t.Fatal("NewConnectionBuilder() returned nil")
	}

	if builder.configReader != configReader {
		t.Error("NewConnectionBuilder() did not set configReader correctly")
	}

	if builder.authProvider != authProvider {
		t.Error("NewConnectionBuilder() did not set authProvider correctly")
	}
}

func TestDefaultConnectionBuilder_Build_NoAuth(t *testing.T) {
	configReader := &mockConfigReader{}
	authProvider := &mockAuthProvider{
		err: errors.New("no authentication method provided"),
	}

	builder := NewConnectionBuilder(configReader, authProvider)
	ctx := context.Background()

	_, err := builder.Build(ctx, "test.host:22", "admin", "", "", nil)
	if err == nil {
		t.Error("Build() expected error for no authentication, got nil")
	}

	if !strings.Contains(err.Error(), "failed to build auth methods") {
		t.Errorf("Build() error = %q, want error containing 'failed to build auth methods'", err.Error())
	}
}

func TestDefaultConnectionBuilder_Build_ConfigReaderError(t *testing.T) {
	configReader := &mockConfigReader{
		err: errors.New("config read failed"),
	}
	authProvider := &mockAuthProvider{
		methods: []ssh.AuthMethod{ssh.Password("test")},
	}

	builder := NewConnectionBuilder(configReader, authProvider)
	ctx := context.Background()

	_, err := builder.Build(ctx, "test.host:22", "admin", "password", "", nil)
	if err == nil {
		t.Error("Build() expected error for config reader failure, got nil")
	}

	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("Build() error = %q, want error containing 'failed to read'", err.Error())
	}
}

func TestDefaultConnectionBuilder_Build_ContextCanceled(t *testing.T) {
	configReader := &mockConfigReader{}
	authProvider := &mockAuthProvider{
		methods: []ssh.AuthMethod{ssh.Password("test")},
	}

	builder := NewConnectionBuilder(configReader, authProvider)

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
	authProvider := &mockAuthProvider{
		methods: []ssh.AuthMethod{ssh.Password("test")},
	}

	builder := NewConnectionBuilder(configReader, authProvider)

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

func TestConnection_IsAlreadyClosedError(t *testing.T) {
	conn := &Connection{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "use of closed network connection",
			err:      errors.New("use of closed network connection"),
			expected: true,
		},
		{
			name:     "connection already closed",
			err:      errors.New("connection already closed"),
			expected: true,
		},
		{
			name:     "error with closed network in message",
			err:      errors.New("ssh: use of closed network connection"),
			expected: true,
		},
		{
			name:     "error with already closed in message",
			err:      errors.New("ssh: connection already closed"),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("network timeout"),
			expected: false,
		},
		{
			name:     "authentication error",
			err:      errors.New("ssh: unable to authenticate"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := conn.IsAlreadyClosedError(tt.err)
			if result != tt.expected {
				t.Errorf("IsAlreadyClosedError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestConnection_GetClient(t *testing.T) {
	conn := &Connection{
		client: nil,
	}

	client := conn.GetClient()
	if client != nil {
		t.Errorf("GetClient() = %v, want nil", client)
	}
}

func TestConnection_Close_NilClient(t *testing.T) {
	conn := &Connection{
		client: nil,
	}

	err := conn.Close()
	if err != nil {
		t.Errorf("Close() with nil client error = %v, want nil", err)
	}
}

func TestConnection_Run_NilClient(t *testing.T) {
	conn := &Connection{
		client: nil,
	}

	_, err := conn.Run("echo test")
	if err == nil {
		t.Error("Run() with nil client expected error, got nil")
	}

	expectedErr := "SSH connection not established"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("Run() error = %q, want %q", err.Error(), expectedErr)
	}
}

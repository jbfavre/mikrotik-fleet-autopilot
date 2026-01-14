package ssh

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestDefaultRunner_IsAlreadyClosedError(t *testing.T) {
	runner := &Runner{}

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
			result := runner.IsAlreadyClosedError(tt.err)
			if result != tt.expected {
				t.Errorf("IsAlreadyClosedError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestDefaultRunner_GetClient(t *testing.T) {
	runner := &Runner{
		client: nil,
	}

	client := runner.getClient()
	if client != nil {
		t.Errorf("getClient() = %v, want nil", client)
	}
}

func TestDefaultRunner_Close_NilClient(t *testing.T) {
	runner := &Runner{
		client: nil,
	}

	err := runner.Close()
	if err != nil {
		t.Errorf("Close() with nil client error = %v, want nil", err)
	}
}

func TestDefaultRunner_Run_NilClient(t *testing.T) {
	runner := &Runner{
		client: nil,
	}

	_, err := runner.Run("echo test")
	if err == nil {
		t.Error("Run() with nil client expected error, got nil")
	}

	expectedErr := "SSH connection not established"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("Run() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestConnection_Run_WithMockServer(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil // Accept any key for testing
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := buildTestConnection(ctx, address, "admin", "testpass", "", hostKeyCallback)
	if err != nil {
		t.Fatalf("Failed to build connection: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	// Test Run
	output, err := conn.Run("echo test")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if output == "" {
		t.Error("Run() returned empty output")
	}

	t.Logf("Run() output: %s", output)
}

func TestConnection_Close_WithMockServer(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := buildTestConnection(ctx, address, "admin", "testpass", "", hostKeyCallback)
	if err != nil {
		t.Fatalf("Failed to build connection: %v", err)
	}

	// Close the connection
	err = conn.Close()
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}

	// Try to close again - should not error (already closed errors are silently ignored)
	err = conn.Close()
	if err != nil {
		t.Logf("Close() twice returned: %v (already closed errors are ignored)", err)
	}
}

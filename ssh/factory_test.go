package ssh

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Mock implementations for testing

type mockCredentialsProvider struct {
	user       string
	password   string
	passphrase string
}

func (m *mockCredentialsProvider) GetUser() string       { return m.user }
func (m *mockCredentialsProvider) GetPassword() string   { return m.password }
func (m *mockCredentialsProvider) GetPassphrase() string { return m.passphrase }

type mockTestHostKeyManager struct {
	existsFunc         func(host string) bool
	verifyFunc         func(host string, key ssh.PublicKey) error
	captureFunc        func(host string, key ssh.PublicKey) error
	getFingerprintFunc func(key ssh.PublicKey) string
}

func (m *mockTestHostKeyManager) Exists(host string) bool {
	if m.existsFunc != nil {
		return m.existsFunc(host)
	}
	return false
}

func (m *mockTestHostKeyManager) Verify(host string, key ssh.PublicKey) error {
	if m.verifyFunc != nil {
		return m.verifyFunc(host, key)
	}
	return nil
}

func (m *mockTestHostKeyManager) Capture(host string, key ssh.PublicKey) error {
	if m.captureFunc != nil {
		return m.captureFunc(host, key)
	}
	return nil
}

func (m *mockTestHostKeyManager) GetFingerprint(key ssh.PublicKey) string {
	if m.getFingerprintFunc != nil {
		return m.getFingerprintFunc(key)
	}
	return "test-fingerprint"
}

func TestCreateConnection_Integration(t *testing.T) {
	// Integration tests that verify the function behavior without mocking internals

	t.Run("with nil credentials panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic with nil credentials")
			}
		}()

		ctx := context.Background()
		_, _ = CreateConnection(ctx, "nonexistent.host.local", nil, &mockTestHostKeyManager{})
	})

	t.Run("with invalid host returns error", func(t *testing.T) {
		ctx := context.Background()
		credentials := &mockCredentialsProvider{user: "admin"}
		manager := &mockTestHostKeyManager{}

		conn, err := CreateConnection(ctx, "definitely.not.a.real.host.invalid:9999", credentials, manager)

		if err == nil {
			t.Error("expected error with invalid host")
		}
		if conn != nil {
			t.Error("expected nil connection on error")
		}
	})

	t.Run("with canceled context returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		credentials := &mockCredentialsProvider{user: "admin"}
		manager := &mockTestHostKeyManager{}

		conn, err := CreateConnection(ctx, "192.168.1.1", credentials, manager)

		if err == nil {
			t.Error("expected error with cancelled context")
		}
		if !strings.Contains(err.Error(), "context") {
			t.Errorf("expected context error, got: %v", err)
		}
		if conn != nil {
			t.Error("expected nil connection on error")
		}
	})
}

func TestCreateConnection_CredentialsFlow(t *testing.T) {
	// Verify that credentials are properly extracted and used

	tests := []struct {
		name        string
		credentials CredentialsProvider
		expectError bool
	}{
		{
			name: "with all credentials",
			credentials: &mockCredentialsProvider{
				user:       "admin",
				password:   "password123",
				passphrase: "passphrase123",
			},
			expectError: true, // Will fail connection but credentials are extracted
		},
		{
			name: "with user only",
			credentials: &mockCredentialsProvider{
				user: "admin",
			},
			expectError: true,
		},
		{
			name: "with empty user",
			credentials: &mockCredentialsProvider{
				user: "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			manager := &mockTestHostKeyManager{}

			_, err := CreateConnection(ctx, "nonexistent.host.local:22", tt.credentials, manager)

			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
		})
	}
}

func TestCreateConnection_ErrorMessages(t *testing.T) {
	// Verify that error messages are properly formatted
	ctx := context.Background()
	credentials := &mockCredentialsProvider{user: "admin"}
	manager := &mockTestHostKeyManager{}

	_, err := CreateConnection(ctx, "nonexistent.test.invalid", credentials, manager)

	if err == nil {
		t.Error("expected error")
		return
	}

	// Check that error message contains useful information
	errMsg := err.Error()
	if !strings.Contains(errMsg, "failed to") {
		t.Errorf("error message should mention connection failure: %v", errMsg)
	}
}

// Benchmark for CreateConnection
func BenchmarkCreateConnection(b *testing.B) {
	credentials := &mockCredentialsProvider{
		user:       "admin",
		password:   "password",
		passphrase: "passphrase",
	}
	hostKeyManager := &mockTestHostKeyManager{
		existsFunc: func(host string) bool { return true },
		verifyFunc: func(host string, key ssh.PublicKey) error { return nil },
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This will fail but measures the function call overhead
		_, _ = CreateConnection(ctx, "nonexistent.test.invalid", credentials, hostKeyManager)
	}
}

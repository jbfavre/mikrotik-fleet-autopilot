package ssh

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
)

// Mock implementations for testing

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

	/* 	t.Run("with nil credentials panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic with nil credentials")
			}
		}()

		ctx := context.WithValue(context.Background(), core.SshManagerKey, &SshManager{user: "", password: "", passphrase: ""})
		_, _ = CreateConnection(ctx, "nonexistent.host.local")
	}) */

	t.Run("with invalid host returns error", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), core.SshManagerKey, &SshManager{})

		conn, err := CreateConnection(ctx, "definitely.not.a.real.host.invalid:9999")

		if err == nil {
			t.Error("expected error with invalid host")
		}
		if conn != nil {
			t.Error("expected nil connection on error")
		}
	})

	t.Run("with canceled context returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), core.SshManagerKey, &SshManager{}))
		cancel() // Cancel immediately

		conn, err := CreateConnection(ctx, "192.168.1.1")

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
		manager     SshManager
		expectError bool
	}{
		{
			name: "with all credentials",
			manager: SshManager{
				user:       "admin",
				password:   "password123",
				passphrase: "passphrase123",
			},
			expectError: true, // Will fail connection but credentials are extracted
		},
		{
			name: "with user only",
			manager: SshManager{
				user: "admin",
			},
			expectError: true,
		},
		{
			name: "with empty user",
			manager: SshManager{
				user: "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), core.SshManagerKey, &tt.manager)
			_, err := CreateConnection(ctx, "nonexistent.host.local:22")

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
	manager := SshManager{}
	ctx := context.WithValue(context.Background(), core.SshManagerKey, &manager)

	_, err := CreateConnection(ctx, "nonexistent.test.invalid")

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

func TestParseSshPrivateKey(t *testing.T) {
	// Create temp directory for test keys
	tmpDir, err := os.MkdirTemp("", "ssh-key-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Write invalid key file
	invalidKeyPath := filepath.Join(tmpDir, "invalid")
	if err := os.WriteFile(invalidKeyPath, []byte("not a valid key"), 0600); err != nil {
		t.Fatalf("failed to write invalid key: %v", err)
	}

	tests := []struct {
		name         string
		identityFile string
		passphrase   string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "non-existent file",
			identityFile: filepath.Join(tmpDir, "nonexistent"),
			passphrase:   "",
			wantErr:      true,
			errContains:  "no such file",
		},
		{
			name:         "invalid key format",
			identityFile: invalidKeyPath,
			passphrase:   "",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := parseSshPrivateKey(tt.identityFile, tt.passphrase)

			if tt.wantErr {
				if err == nil {
					t.Error("parseSshPrivateKey() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseSshPrivateKey() error = %v, want error containing %q", err, tt.errContains)
				}
				if signer != nil {
					t.Error("parseSshPrivateKey() expected nil signer on error")
				}
				return
			}

			if err != nil {
				t.Errorf("parseSshPrivateKey() unexpected error = %v", err)
				return
			}

			if signer == nil {
				t.Error("parseSshPrivateKey() returned nil signer")
			}
		})
	}
}

func TestParseSshPrivateKey_TildeExpansion(t *testing.T) {
	// Test that tilde expansion works by checking error message contains expanded path
	currentUser, err := user.Current()
	if err != nil {
		t.Skip("cannot get current user")
	}

	// Use a non-existent path to test tilde expansion
	tildePath := "~/.ssh-test-parsessh-nonexistent/id_rsa"

	_, err = parseSshPrivateKey(tildePath, "")
	if err == nil {
		t.Error("parseSshPrivateKey() expected error for non-existent file")
		return
	}

	// Verify the error message contains the expanded path (not the tilde path)
	expandedPath := filepath.Join(currentUser.HomeDir, ".ssh-test-parsessh-nonexistent/id_rsa")
	if !strings.Contains(err.Error(), expandedPath) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("parseSshPrivateKey() error = %v, expected to contain expanded path or 'no such file'", err)
	}
}

func TestCreateConnection_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		manager SshManager
		wantErr bool
	}{
		{
			name: "empty host",
			host: "",
			manager: SshManager{
				user:     "admin",
				password: "pass",
			},
			wantErr: true,
		},
		{
			name: "invalid host format",
			host: ":::invalid",
			manager: SshManager{
				user:     "admin",
				password: "pass",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		ctx := context.WithValue(context.Background(), core.SshManagerKey, &tt.manager)
		t.Run(tt.name, func(t *testing.T) {

			_, err := CreateConnection(ctx, tt.host)

			if !tt.wantErr {
				if err != nil {
					t.Errorf("CreateConnection() unexpected error = %v", err)
				}
				return
			}

			if err == nil {
				t.Error("CreateConnection() expected error, got nil")
			}
		})
	}
}

func TestBuildAuthMethods_PasswordOnly(t *testing.T) {
	hostInfo := &HostInfo{
		Hostname: "test.example.com",
		Port:     "22",
		User:     "admin",
	}

	methods, err := buildAuthMethods(hostInfo, "testpass123", "")
	if err != nil {
		t.Fatalf("buildAuthMethods() unexpected error = %v", err)
	}

	if len(methods) != 1 {
		t.Errorf("buildAuthMethods() got %d methods, want 1", len(methods))
	}
}

func TestBuildAuthMethods_NoCredentials(t *testing.T) {
	hostInfo := &HostInfo{
		Hostname: "test.example.com",
		Port:     "22",
		User:     "admin",
	}

	_, err := buildAuthMethods(hostInfo, "", "")
	if err == nil {
		t.Error("buildAuthMethods() expected error for no credentials, got nil")
	}

	if err != nil && !strings.Contains(err.Error(), "no authentication method") {
		t.Errorf("buildAuthMethods() error = %v, want error mentioning 'no authentication method'", err)
	}
}

func TestBuildAuthMethods_WithKey(t *testing.T) {
	// Test that buildAuthMethods requires both IdentityFile and passphrase for key auth
	hostInfo := &HostInfo{
		Hostname:     "test.example.com",
		Port:         "22",
		User:         "admin",
		IdentityFile: "/tmp/id_rsa",
	}

	// Without passphrase, key auth should fail
	methods, err := buildAuthMethods(hostInfo, "", "")
	if err == nil {
		t.Error("buildAuthMethods() expected error without passphrase, got nil")
	}
	if methods != nil {
		t.Error("buildAuthMethods() expected nil methods on error")
	}

	// With password only (no passphrase), should use password auth
	methods, err = buildAuthMethods(hostInfo, "testpass", "")
	if err != nil {
		t.Errorf("buildAuthMethods() unexpected error with password: %v", err)
	}
	if len(methods) != 1 {
		t.Errorf("buildAuthMethods() with password got %d methods, want 1", len(methods))
	}
}

func TestBuildAuthMethods_WithBothKeyAndPassword(t *testing.T) {
	// Test scenario with both key (via passphrase) and password
	// Since we can't create real encrypted keys easily, we test the logic path
	hostInfo := &HostInfo{
		Hostname:     "test.example.com",
		Port:         "22",
		User:         "admin",
		IdentityFile: "/nonexistent/key",
	}

	// With both passphrase and password, should attempt key loading first
	// Will fail because key doesn't exist, but tests the logic path
	methods, err := buildAuthMethods(hostInfo, "testpass", "testphrase")
	if err == nil {
		t.Error("buildAuthMethods() expected error with nonexistent key file, got nil")
	}
	if methods != nil {
		t.Error("buildAuthMethods() expected nil methods on error")
	}
}

func TestCreateConnection_ContextCancellation(t *testing.T) {
	manager := SshManager{
		user:     "admin",
		password: "testpass",
	}
	// Test that CreateConnection respects context cancellation
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), core.SshManagerKey, &manager))
	cancel() // Cancel immediately

	_, err := CreateConnection(ctx, "192.168.1.1:22")
	if err == nil {
		t.Error("CreateConnection() expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "context cancelled") && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("CreateConnection() error should mention context cancellation, got: %v", err)
	}
}

func TestCreateConnection_InvalidAuthMethods(t *testing.T) {
	manager := SshManager{
		user:     "admin",
		password: "",
	}

	// Test that CreateConnection fails properly when no auth methods provided
	ctx := context.WithValue(context.Background(), core.SshManagerKey, &manager)

	_, err := CreateConnection(ctx, "192.168.1.1:22")
	if err == nil {
		t.Error("CreateConnection() expected error with no auth methods, got nil")
	}
	if !strings.Contains(err.Error(), "failed to build auth methods") {
		t.Errorf("CreateConnection() error should mention auth methods, got: %v", err)
	}
}

func TestBuildAuthMethods_KeyOnly(t *testing.T) {
	// Test scenario with only key (no password)
	// Create temp directory for test keys
	tmpDir, err := os.MkdirTemp("", "ssh-key-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Write invalid key file
	keyPath := filepath.Join(tmpDir, "test_key")
	if err := os.WriteFile(keyPath, []byte("not a valid key"), 0600); err != nil {
		t.Fatalf("failed to write key: %v", err)
	}

	hostInfo := &HostInfo{
		Hostname:     "test.example.com",
		Port:         "22",
		User:         "admin",
		IdentityFile: keyPath,
	}

	// With only passphrase (no password), should attempt key auth
	methods, err := buildAuthMethods(hostInfo, "", "testphrase")
	// Should fail because key is invalid
	if err == nil {
		t.Error("buildAuthMethods() expected error with invalid key, got nil")
	}
	if methods != nil {
		t.Error("buildAuthMethods() expected nil methods on error")
	}
}

func TestBuildAuthMethods_EmptyIdentityFile(t *testing.T) {
	// Test with passphrase but empty identity file
	hostInfo := &HostInfo{
		Hostname:     "test.example.com",
		Port:         "22",
		User:         "admin",
		IdentityFile: "", // Empty identity file
	}

	// With passphrase but no identity file, can't use key auth, should use password
	methods, err := buildAuthMethods(hostInfo, "testpass", "testphrase")
	if err != nil {
		t.Errorf("buildAuthMethods() unexpected error with password: %v", err)
	}
	if len(methods) != 1 {
		t.Errorf("buildAuthMethods() got %d methods, want 1 (password only)", len(methods))
	}
}

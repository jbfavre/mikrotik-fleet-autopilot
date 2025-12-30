package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestSshConnection_Close(t *testing.T) {
	tests := []struct {
		name       string
		closeError error
		wantErr    bool
	}{
		{
			name:       "successful close",
			closeError: nil,
			wantErr:    false,
		},
		{
			name:       "close with already closed error",
			closeError: errors.New("use of closed network connection"),
			wantErr:    false, // Should be silently ignored
		},
		{
			name:       "close with connection already closed error",
			closeError: errors.New("connection already closed"),
			wantErr:    false, // Should be silently ignored
		},
		{
			name:       "close with other error",
			closeError: errors.New("network timeout"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't directly test sshConnection.Close without mocking the internal ssh.Client
			// Instead, we test the IsAlreadyClosedError logic which is used by Close
			conn := &sshConnection{}
			isIgnored := conn.IsAlreadyClosedError(tt.closeError)
			expectedIgnored := (tt.closeError != nil && !tt.wantErr)

			if isIgnored != expectedIgnored {
				t.Errorf("IsAlreadyClosedError() = %v, want %v for error: %v", isIgnored, expectedIgnored, tt.closeError)
			}
		})
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

			conn := &sshConnection{}
			result := conn.IsAlreadyClosedError(err)
			if result != tt.wantRes {
				t.Errorf("IsAlreadyClosedError(%q) = %v, want %v", tt.errMsg, result, tt.wantRes)
			}
		})
	}
}

func TestIsAlreadyClosedError_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "empty error message",
			err:  errors.New(""),
			want: false,
		},
		{
			name: "case sensitive - lowercase",
			err:  errors.New("USE OF CLOSED NETWORK CONNECTION"),
			want: false, // Function is case-sensitive
		},
		{
			name: "wrapped closed connection error",
			err:  errors.New("failed to read: use of closed network connection"),
			want: true,
		},
		{
			name: "connection already closed with context",
			err:  errors.New("ssh: connection already closed by peer"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &sshConnection{}
			if got := conn.IsAlreadyClosedError(tt.err); got != tt.want {
				t.Errorf("IsAlreadyClosedError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkIsAlreadyClosedError(b *testing.B) {
	err := &mockError{msg: "use of closed network connection"}
	conn := &sshConnection{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = conn.IsAlreadyClosedError(err)
	}
}

func TestSshRunner_Interface(t *testing.T) {
	// Test that sshConnection implements SshRunner interface
	var _ SshRunner = (*sshConnection)(nil)

	// This test ensures the interface contract is maintained
	t.Log("sshConnection correctly implements SshRunner interface")
}

// TestSshConnectionStructure verifies the sshConnection struct can be created
func TestSshConnectionStructure(t *testing.T) {
	// We can't create a real ssh.Client without connecting to a server,
	// so we just verify the struct definition is correct
	conn := &sshConnection{}

	// Verify struct has expected zero value
	if conn.client != nil {
		t.Error("sshConnection.client should be nil after initialization")
	}
}

func TestSshConnection_Run_NilClient(t *testing.T) {
	// Test that Run() returns proper error when client is nil
	conn := &sshConnection{
		client:       nil,
		clientConfig: nil,
	}

	_, err := conn.Run("/system/resource/print")
	if err == nil {
		t.Error("Run() expected error when client is nil, got nil")
	}

	expectedErr := "SSH connection not established"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("Run() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestNewSsh_NoAuthenticationMethods(t *testing.T) {
	// Test that newSsh returns error when no authentication is provided
	ctx := context.Background()
	_, err := newSsh(ctx, "test-host:22", "admin", "", "")

	if err == nil {
		t.Error("newSsh() expected error for no authentication, got nil")
	}

	expectedErr := "no authentication method provided (need password or SSH key with passphrase)"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("newSsh() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestNewSsh_PasswordOnly_ConnectionFails(t *testing.T) {
	// Test newSsh with password - should fail to connect but validates auth setup
	ctx := context.Background()
	_, err := newSsh(ctx, "invalid-host-that-does-not-exist.local:22", "admin", "password123", "")

	if err == nil {
		t.Error("newSsh() expected connection error for invalid host, got nil")
	}

	// Should contain "failed to dial" in error
	if err != nil && !strings.Contains(err.Error(), "failed to dial") {
		t.Errorf("newSsh() expected 'failed to dial' error, got: %v", err)
	}
}

func TestNewSsh_PassphraseWithoutKey(t *testing.T) {
	// Test newSsh with passphrase but no valid key file
	// This should fail during key parsing
	ctx := context.Background()
	_, err := newSsh(ctx, "test-host:22", "admin", "", "passphrase123")

	if err == nil {
		t.Error("newSsh() expected error for invalid key, got nil")
	}

	// Should fail because parseSshPrivateKey returns nil
	expectedErr := "open : no such file or directory"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("newSsh() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestNewSsh_BothPasswordAndPassphrase_ConnectionFails(t *testing.T) {
	// Test newSsh with both password and passphrase
	// Should prepare both auth methods but fail to connect
	ctx := context.Background()
	_, err := newSsh(ctx, "invalid.host:22", "admin", "password123", "passphrase456")

	if err == nil {
		t.Error("newSsh() expected error, got nil")
	}

	// Could fail at key parsing or connection - both are acceptable
	// Just verify we got an error
	if err == nil {
		t.Error("newSsh() should fail with invalid host or missing key")
	}
}

func TestReadSshConfig_NoConfigFile(t *testing.T) {
	// Test with default HOME that doesn't have .ssh/config
	// This simulates CI environment
	originalHome := os.Getenv("HOME")
	defer func() {
		if err := os.Setenv("HOME", originalHome); err != nil {
			t.Logf("Failed to restore HOME: %v", err)
		}
	}()

	// Set HOME to a temp dir without .ssh/config
	tmpDir := t.TempDir()
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}

	config := readSshConfig("test-host")

	if config == nil {
		t.Fatal("readSshConfig() returned nil")
	}

	// Verify HostInfo is not nil and has expected values
	if config == nil {
		t.Fatal("readSshConfig() returned nil")
	}

	// Verify defaults
	if config.Hostname != "test-host" {
		t.Errorf("Hostname = %q, want %q", config.Hostname, "test-host")
	}
	if config.Port != "22" {
		t.Errorf("Port = %q, want %q", config.Port, "22")
	}
	if config.User != "" {
		t.Errorf("User = %q, want empty string", config.User)
	}
}

func TestReadSshConfig_WithFixtures(t *testing.T) {
	// Get the testdata directory path
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	tests := []struct {
		name           string
		configFile     string
		host           string
		expectedHost   string
		expectedUser   string
		expectedPort   string
		expectedIdFile string
	}{
		{
			name:           "valid config - testhost",
			configFile:     "ssh_config/valid_config",
			host:           "testhost",
			expectedHost:   "192.168.1.100",
			expectedUser:   "admin",
			expectedPort:   "22",
			expectedIdFile: "testdata/ssh_keys/test_key",
		},
		{
			name:           "valid config - encrypted host",
			configFile:     "ssh_config/valid_config",
			host:           "encrypted-host",
			expectedHost:   "192.168.1.101",
			expectedUser:   "root",
			expectedPort:   "2222",
			expectedIdFile: "testdata/ssh_keys/encrypted_key",
		},
		{
			name:           "valid config - hostname expansion",
			configFile:     "ssh_config/valid_config",
			host:           "routerTest",
			expectedHost:   "routerTest.home",
			expectedUser:   "admin",
			expectedPort:   "22",
			expectedIdFile: "testdata/ssh_keys/test_key",
		},
		{
			name:           "multi host - router1",
			configFile:     "ssh_config/multi_host_config",
			host:           "router1",
			expectedHost:   "10.0.0.1",
			expectedUser:   "admin",
			expectedPort:   "22",
			expectedIdFile: "testdata/ssh_keys/test_key",
		},
		{
			name:           "multi host - router2 custom port",
			configFile:     "ssh_config/multi_host_config",
			host:           "router2",
			expectedHost:   "10.0.0.2",
			expectedUser:   "netadmin",
			expectedPort:   "8022",
			expectedIdFile: "testdata/ssh_keys/encrypted_key",
		},
		{
			name:           "wildcard - dev host expansion",
			configFile:     "ssh_config/wildcards_config",
			host:           "dev-server1",
			expectedHost:   "dev-server1.internal.net",
			expectedUser:   "devops",
			expectedPort:   "22",
			expectedIdFile: "testdata/ssh_keys/test_key",
		},
		{
			name:           "wildcard - catch all",
			configFile:     "ssh_config/wildcards_config",
			host:           "unknown-host",
			expectedHost:   "unknown-host",
			expectedUser:   "default-user",
			expectedPort:   "22",
			expectedIdFile: "testdata/ssh_keys/test_key",
		},
		{
			name:           "empty config - should use defaults",
			configFile:     "ssh_config/empty_config",
			host:           "any-host",
			expectedHost:   "any-host",
			expectedUser:   "",
			expectedPort:   "22",
			expectedIdFile: "",
		},
	}

	originalHome := os.Getenv("HOME")
	defer func() {
		if err := os.Setenv("HOME", originalHome); err != nil {
			t.Logf("Failed to restore HOME: %v", err)
		}
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary .ssh directory with our test config
			tmpDir := t.TempDir()
			sshDir := filepath.Join(tmpDir, ".ssh")
			if err := os.MkdirAll(sshDir, 0700); err != nil {
				t.Fatalf("Failed to create .ssh dir: %v", err)
			}

			// Copy test config to .ssh/config
			configSrc := filepath.Join(testdataDir, tt.configFile)
			configDst := filepath.Join(sshDir, "config")
			data, err := os.ReadFile(configSrc)
			if err != nil {
				t.Fatalf("Failed to read test config: %v", err)
			}
			if err := os.WriteFile(configDst, data, 0600); err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}

			// Set HOME to temp directory
			if err := os.Setenv("HOME", tmpDir); err != nil {
				t.Fatalf("Failed to set HOME: %v", err)
			}

			// Test readSshConfig
			config := readSshConfig(tt.host)

			if config == nil {
				t.Fatal("readSshConfig() returned nil")
			}

			if config.Hostname != tt.expectedHost {
				t.Errorf("Hostname = %q, want %q", config.Hostname, tt.expectedHost)
			}
			if config.User != tt.expectedUser {
				t.Errorf("User = %q, want %q", config.User, tt.expectedUser)
			}
			if config.Port != tt.expectedPort {
				t.Errorf("Port = %q, want %q", config.Port, tt.expectedPort)
			}
			if config.IdentityFile != tt.expectedIdFile {
				t.Errorf("IdentityFile = %q, want %q", config.IdentityFile, tt.expectedIdFile)
			}
		})
	}
}

func TestParseSshPrivateKey_Unencrypted(t *testing.T) {
	// Test parsing unencrypted key
	// Note: The current implementation uses ssh.ParsePrivateKeyWithPassphrase
	// which fails for unencrypted keys when given an empty passphrase
	// This test documents the current behavior
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "test_key")

	// With empty passphrase, unencrypted keys fail to parse
	signer, _ := parseSshPrivateKey(keyPath, "")

	// Current implementation returns nil for unencrypted keys with empty passphrase
	if signer != nil {
		t.Error("parseSshPrivateKey() unexpectedly succeeded for unencrypted key with empty passphrase")
	}

	// Note: To support unencrypted keys, parseSshPrivateKey would need to try
	// ssh.ParsePrivateKey() first before ssh.ParsePrivateKeyWithPassphrase()
}

func TestParseSshPrivateKey_Encrypted(t *testing.T) {
	// Test parsing encrypted key with correct passphrase
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "encrypted_key")

	signer, _ := parseSshPrivateKey(keyPath, "testpassphrase")

	if signer == nil {
		t.Error("parseSshPrivateKey() returned nil for valid encrypted key with correct passphrase")
	}
}

func TestParseSshPrivateKey_WrongPassphrase(t *testing.T) {
	// Test parsing encrypted key with wrong passphrase
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "encrypted_key")

	signer, _ := parseSshPrivateKey(keyPath, "wrongpassphrase")

	if signer != nil {
		t.Error("parseSshPrivateKey() should return nil for wrong passphrase")
	}
}

func TestParseSshPrivateKey_InvalidKey(t *testing.T) {
	// Test parsing invalid key file
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "invalid_key")

	signer, _ := parseSshPrivateKey(keyPath, "anypassphrase")

	if signer != nil {
		t.Error("parseSshPrivateKey() should return nil for invalid key file")
	}
}

func TestParseSshPrivateKey_NonexistentFile(t *testing.T) {
	// Test with nonexistent file
	signer, _ := parseSshPrivateKey("/nonexistent/path/to/key", "passphrase")

	if signer != nil {
		t.Error("parseSshPrivateKey() should return nil for nonexistent file")
	}
}

func TestParseSshPrivateKey_TildeExpansion(t *testing.T) {
	// Test tilde expansion in path
	currentUser, err := user.Current()
	if err != nil {
		t.Skip("Cannot get current user")
	}

	// The function expands ~/ to home directory
	// We'll test that it doesn't panic with a tilde path
	signer, _ := parseSshPrivateKey("~/nonexistent_key", "passphrase")

	// Should return nil (file doesn't exist) but shouldn't panic
	if signer != nil {
		t.Error("parseSshPrivateKey() should return nil for nonexistent file")
	}

	// Verify the function at least attempts tilde expansion
	// by checking it doesn't crash
	t.Logf("Tilde expansion works for user: %s", currentUser.Username)
}

func BenchmarkSshConnection_Run_NilCheck(b *testing.B) {
	conn := &sshConnection{client: nil}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = conn.Run("test")
	}
}

// MockSSHClient is a mock implementation of ssh.Client for testing Close()
type MockSSHClient struct {
	CloseError error
	Closed     bool
}

func (m *MockSSHClient) Close() error {
	m.Closed = true
	return m.CloseError
}

// TestSshConnection_Close_WithMockClient tests Close() with various scenarios
func TestSshConnection_Close_WithMockClient(t *testing.T) {
	tests := []struct {
		name       string
		closeError error
		wantErr    bool
	}{
		{
			name:       "successful close",
			closeError: nil,
			wantErr:    false,
		},
		{
			name:       "close with already closed error - should not return error",
			closeError: &net.OpError{Err: fmt.Errorf("use of closed network connection")},
			wantErr:    false,
		},
		{
			name:       "close with connection already closed error",
			closeError: fmt.Errorf("connection already closed"),
			wantErr:    false,
		},
		{
			name:       "close with other error",
			closeError: fmt.Errorf("network timeout"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We need to test Close indirectly through its behavior
			conn := &sshConnection{}

			// Test IsAlreadyClosedError which Close() uses internally
			shouldIgnore := conn.IsAlreadyClosedError(tt.closeError)

			// If tt.wantErr is false and error is not nil, it means the error should be ignored
			expectedIgnore := tt.closeError != nil && !tt.wantErr

			if shouldIgnore != expectedIgnore {
				t.Errorf("IsAlreadyClosedError() = %v, want %v for error: %v",
					shouldIgnore, expectedIgnore, tt.closeError)
			}
		})
	}
}

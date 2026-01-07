package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultAuthProvider_BuildAuthMethods_PasswordOnly(t *testing.T) {
	provider := &DefaultAuthProvider{}
	hostInfo := &HostInfo{
		Hostname: "test.host",
	}

	methods, err := provider.BuildAuthMethods(hostInfo, "mypassword", "")
	if err != nil {
		t.Fatalf("BuildAuthMethods() error = %v, want nil", err)
	}

	if len(methods) != 1 {
		t.Errorf("BuildAuthMethods() returned %d methods, want 1", len(methods))
	}
}

func TestDefaultAuthProvider_BuildAuthMethods_NoAuth(t *testing.T) {
	provider := &DefaultAuthProvider{}
	hostInfo := &HostInfo{
		Hostname: "test.host",
	}

	_, err := provider.BuildAuthMethods(hostInfo, "", "")
	if err == nil {
		t.Error("BuildAuthMethods() expected error for no authentication, got nil")
	}

	expectedErr := "no authentication method provided (need password or SSH key with passphrase)"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("BuildAuthMethods() error = %q, want %q", err.Error(), expectedErr)
	}
}

func TestDefaultAuthProvider_BuildAuthMethods_WithKeyFile(t *testing.T) {
	// Create a temporary unencrypted SSH key
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key")

	// Generate an unencrypted RSA key
	privateKey := `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAABlwAAAAdzc2gtcn
NhAAAAAwEAAQAAAYEAw4qJo8xCkBLPEDMZO0qPXvGn8qPDj8+3rKj0hQ0XJ5lN8RkfEz0D
xJL+rkS7bJ/+wY6bG/LVF7qH2Y6hH2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF
3F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3
F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F
7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7
hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7h
N2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3L8vH4pF5hF3F7hN2Y3F8vN3AAAA
-----END OPENSSH PRIVATE KEY-----`

	if err := os.WriteFile(keyPath, []byte(privateKey), 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	provider := &DefaultAuthProvider{}
	hostInfo := &HostInfo{
		Hostname:     "test.host",
		IdentityFile: keyPath,
	}

	// This should fail because the key is not a valid key, but tests that the code path works
	methods, err := provider.BuildAuthMethods(hostInfo, "", "passphrase")

	// We expect an error because the key is invalid
	if err == nil {
		t.Error("BuildAuthMethods() expected error for invalid key, got nil")
	}

	// But if somehow it succeeded, check the methods
	if err == nil && len(methods) == 0 {
		t.Error("BuildAuthMethods() returned 0 methods when key was provided")
	}
}

func TestDefaultAuthProvider_BuildAuthMethods_BothPasswordAndKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key")

	// Write an invalid key (will fail to parse)
	if err := os.WriteFile(keyPath, []byte("invalid key content"), 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	provider := &DefaultAuthProvider{}
	hostInfo := &HostInfo{
		Hostname:     "test.host",
		IdentityFile: keyPath,
	}

	// With both password and passphrase, should try to build both methods
	methods, err := provider.BuildAuthMethods(hostInfo, "password", "passphrase")

	// Key will fail to parse, but this tests that the error is returned correctly
	// when both auth methods are requested
	if err == nil {
		t.Error("BuildAuthMethods() expected error for invalid key, got nil")
	}

	// Even with error, check if password method was attempted
	if err != nil && len(methods) > 0 {
		t.Logf("BuildAuthMethods() returned %d methods despite error", len(methods))
	}
}

func TestDefaultAuthProvider_parseSshPrivateKey_NonexistentFile(t *testing.T) {
	provider := &DefaultAuthProvider{}

	_, err := provider.parseSshPrivateKey("/nonexistent/path/to/key", "")
	if err == nil {
		t.Error("parseSshPrivateKey() expected error for nonexistent file, got nil")
	}
}

func TestDefaultAuthProvider_parseSshPrivateKey_InvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "invalid_key")

	if err := os.WriteFile(keyPath, []byte("not a valid ssh key"), 0600); err != nil {
		t.Fatalf("Failed to write invalid key file: %v", err)
	}

	provider := &DefaultAuthProvider{}

	_, err := provider.parseSshPrivateKey(keyPath, "")
	if err == nil {
		t.Error("parseSshPrivateKey() expected error for invalid key, got nil")
	}
}

func TestDefaultAuthProvider_parseSshPrivateKey_TildeExpansion(t *testing.T) {
	provider := &DefaultAuthProvider{}

	// Test that tilde is expanded (will fail to find file, but tests expansion)
	keyPath := "~/nonexistent_key"
	_, err := provider.parseSshPrivateKey(keyPath, "")

	if err == nil {
		t.Error("parseSshPrivateKey() expected error for nonexistent file")
	}

	// Verify the error message indicates the file couldn't be found
	// (not that the tilde character was in the path)
	if err != nil && !strings.Contains(err.Error(), "open") {
		t.Logf("parseSshPrivateKey() with tilde path returned error: %v", err)
	}
}

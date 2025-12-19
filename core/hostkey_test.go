package core

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// generateTestKey generates an RSA key pair for testing
func generateTestKey() (ssh.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, err
	}

	return publicKey, nil
}

func TestHostKeyFilePath(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{
			name:     "simple hostname",
			host:     "router1",
			expected: "router1.hostkey",
		},
		{
			name:     "FQDN",
			host:     "router1.home.local",
			expected: "router1.hostkey",
		},
		{
			name:     "IP address",
			host:     "192.168.1.1",
			expected: "192.168.1.1.hostkey",
		},
		{
			name:     "hostname with port",
			host:     "router1:2222",
			expected: "router1.hostkey",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HostKeyFilePath(tt.host)
			if result != tt.expected {
				t.Errorf("HostKeyFilePath(%s) = %s, want %s", tt.host, result, tt.expected)
			}
		})
	}
}

func TestGetHostKeyFingerprint(t *testing.T) {
	key, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	fp := GetHostKeyFingerprint(key)

	// Check format
	if len(fp) < 8 || fp[:7] != "SHA256:" {
		t.Errorf("GetHostKeyFingerprint() = %s, expected format SHA256:...", fp)
	}

	// Should be consistent
	fp2 := GetHostKeyFingerprint(key)
	if fp != fp2 {
		t.Errorf("GetHostKeyFingerprint() not consistent: %s != %s", fp, fp2)
	}
}

func TestCaptureAndLoadHostKey(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Logf("Failed to restore directory: %v", err)
		}
	}()

	// Generate test key
	testKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "testrouter"

	// Capture key
	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Check file exists
	if !HostKeyExists(host) {
		t.Error("HostKeyExists() = false after capture, want true")
	}

	// Load key
	loadedKey, err := LoadHostKey(host)
	if err != nil {
		t.Fatalf("LoadHostKey() failed: %v", err)
	}

	// Compare keys
	if testKey.Type() != loadedKey.Type() {
		t.Errorf("Key type mismatch: got %s, want %s", loadedKey.Type(), testKey.Type())
	}

	testBytes := testKey.Marshal()
	loadedBytes := loadedKey.Marshal()

	if len(testBytes) != len(loadedBytes) {
		t.Errorf("Key length mismatch: got %d, want %d", len(loadedBytes), len(testBytes))
	}

	for i := range testBytes {
		if testBytes[i] != loadedBytes[i] {
			t.Errorf("Key bytes differ at position %d", i)
			break
		}
	}
}

func TestVerifyHostKey(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Logf("Failed to restore directory: %v", err)
		}
	}()

	// Generate test keys
	correctKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	wrongKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate wrong key: %v", err)
	}

	host := "testrouter"

	// Capture correct key
	if err := CaptureHostKey(host, correctKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Verify with correct key - should succeed
	if err := VerifyHostKey(host, correctKey); err != nil {
		t.Errorf("VerifyHostKey() with correct key failed: %v", err)
	}

	// Verify with wrong key - should fail
	if err := VerifyHostKey(host, wrongKey); err == nil {
		t.Error("VerifyHostKey() with wrong key succeeded, want error")
	}
}

func TestHostKeyExists(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Logf("Failed to restore directory: %v", err)
		}
	}()

	host := "testrouter"

	// Should not exist initially
	if HostKeyExists(host) {
		t.Error("HostKeyExists() = true before creation, want false")
	}

	// Create key
	testKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Should exist now
	if !HostKeyExists(host) {
		t.Error("HostKeyExists() = false after creation, want true")
	}
}

func TestDeleteHostKey(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	// Try to delete non-existent key
	if err := DeleteHostKey(host); err == nil {
		t.Error("DeleteHostKey() on non-existent key succeeded, want error")
	}

	// Create key
	testKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Delete key
	if err := DeleteHostKey(host); err != nil {
		t.Errorf("DeleteHostKey() failed: %v", err)
	}

	// Should not exist anymore
	if HostKeyExists(host) {
		t.Error("HostKeyExists() = true after deletion, want false")
	}
}

func TestBackupHostKey(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	// Try to backup non-existent key
	if err := BackupHostKey(host); err == nil {
		t.Error("BackupHostKey() on non-existent key succeeded, want error")
	}

	// Create key
	testKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Backup key
	if err := BackupHostKey(host); err != nil {
		t.Errorf("BackupHostKey() failed: %v", err)
	}

	// Check backup file exists
	backupPath := HostKeyFilePath(host) + ".backup"
	if _, err := os.Stat(backupPath); err != nil {
		t.Errorf("Backup file does not exist: %v", err)
	}

	// Verify backup content matches original
	originalData, _ := os.ReadFile(HostKeyFilePath(host))
	backupData, _ := os.ReadFile(backupPath)

	if len(originalData) != len(backupData) {
		t.Error("Backup file size differs from original")
	}
}

func TestLoadHostKeyInfo(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	// Generate and capture key
	testKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Load host key info
	info, err := LoadHostKeyInfo(host)
	if err != nil {
		t.Fatalf("LoadHostKeyInfo() failed: %v", err)
	}

	// Verify fields
	if info.Host != host {
		t.Errorf("Host = %s, want %s", info.Host, host)
	}

	if info.Algorithm != testKey.Type() {
		t.Errorf("Algorithm = %s, want %s", info.Algorithm, testKey.Type())
	}

	expectedFp := GetHostKeyFingerprint(testKey)
	if info.Fingerprint != expectedFp {
		t.Errorf("Fingerprint = %s, want %s", info.Fingerprint, expectedFp)
	}

	// Check timestamp is recent
	if time.Since(info.CapturedAt) > time.Minute {
		t.Errorf("CapturedAt = %v, seems too old", info.CapturedAt)
	}
}

func TestHostKeyFileFormat(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	// Generate and capture key
	testKey, err := generateTestKey()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	// Read and parse file manually
	data, err := os.ReadFile(HostKeyFilePath(host))
	if err != nil {
		t.Fatalf("Failed to read host key file: %v", err)
	}

	var info HostKeyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("Failed to unmarshal host key file: %v", err)
	}

	// Verify all fields are present
	if info.Host == "" {
		t.Error("Host field is empty")
	}
	if info.Algorithm == "" {
		t.Error("Algorithm field is empty")
	}
	if info.Fingerprint == "" {
		t.Error("Fingerprint field is empty")
	}
	if info.PublicKey == "" {
		t.Error("PublicKey field is empty")
	}
	if info.CapturedAt.IsZero() {
		t.Error("CapturedAt field is zero")
	}
}

func TestLoadHostKeyInvalidFile(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	// Create invalid JSON file
	path := HostKeyFilePath(host)
	if err := os.WriteFile(path, []byte("invalid json"), 0600); err != nil {
		t.Fatalf("Failed to write invalid file: %v", err)
	}

	// Try to load - should fail
	if _, err := LoadHostKey(host); err == nil {
		t.Error("LoadHostKey() with invalid JSON succeeded, want error")
	}
}

func TestLoadHostKeyCorruptedKey(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Change to temp directory
	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	// Create file with corrupted key
	info := HostKeyInfo{
		Host:        host,
		CapturedAt:  time.Now(),
		Algorithm:   "ssh-rsa",
		Fingerprint: "SHA256:test",
		PublicKey:   "invalid-base64!!!",
	}

	data, _ := json.Marshal(info)
	path := HostKeyFilePath(host)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Try to load - should fail
	if _, err := LoadHostKey(host); err == nil {
		t.Error("LoadHostKey() with invalid base64 succeeded, want error")
	}
}

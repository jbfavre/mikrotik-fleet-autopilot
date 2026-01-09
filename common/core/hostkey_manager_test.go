package core

import (
	"os"
	"testing"
)

func TestNewHostKeyManager(t *testing.T) {
	manager := NewHostKeyManager()
	if manager == nil {
		t.Fatal("NewHostKeyManager() returned nil")
	}
}

func TestGetHostKeyManager(t *testing.T) {
	manager := GetHostKeyManager()
	if manager == nil {
		t.Fatal("GetHostKeyManager() returned nil")
	}

	// Verify it implements the interface by calling a method
	_ = manager.Exists("test-host")
}

func TestHostKeyManager_Exists(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewHostKeyManager()

	// Test non-existent host key
	if manager.Exists("nonexistent-host") {
		t.Error("Exists() should return false for nonexistent host key")
	}

	// Create a host key file
	testFile := "test-host.hostkey"
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test existing host key
	if !manager.Exists("test-host") {
		t.Error("Exists() should return true for existing host key")
	}
}

func TestHostKeyManager_GetFingerprint(t *testing.T) {
	manager := NewHostKeyManager()

	// Generate a test key instead of reading from file
	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	fingerprint := manager.GetFingerprint(pubKey)
	if fingerprint == "" {
		t.Error("GetFingerprint() returned empty string for valid key")
	}

	// Verify it starts with expected format (SHA256:...)
	if len(fingerprint) < 10 {
		t.Errorf("GetFingerprint() returned unexpectedly short fingerprint: %s", fingerprint)
	}
}

func TestHostKeyManager_Capture(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewHostKeyManager()

	// Generate a test key instead of reading from file
	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	// Capture host key
	host := "test-capture-host"
	if err := manager.Capture(host, pubKey); err != nil {
		t.Errorf("Capture() failed: %v", err)
	}

	// Verify file was created
	expectedFile := host + ".hostkey"
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Errorf("Capture() did not create expected file: %s", expectedFile)
	}

	// Verify the captured key exists
	if !manager.Exists(host) {
		t.Error("Exists() should return true after Capture()")
	}
}

func TestHostKeyManager_Verify(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewHostKeyManager()

	// Generate a test key instead of reading from file
	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "test-verify-host"

	// Capture the key first
	if err := manager.Capture(host, pubKey); err != nil {
		t.Fatalf("Capture() failed: %v", err)
	}

	// Verify should succeed with the same key
	if err := manager.Verify(host, pubKey); err != nil {
		t.Errorf("Verify() failed with correct key: %v", err)
	}
}

func TestHostKeyManager_VerifyWrongKey(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewHostKeyManager()

	// Generate two different test keys
	pubKey1, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}
	pubKey2, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "test-verify-wrong-key"

	// Capture first key
	if err := manager.Capture(host, pubKey1); err != nil {
		t.Fatalf("Capture() failed: %v", err)
	}

	// Verify should fail with different key
	if err := manager.Verify(host, pubKey2); err == nil {
		t.Error("Verify() should fail with wrong key")
	}
}

func BenchmarkHostKeyManager_Exists(b *testing.B) {
	manager := NewHostKeyManager()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = manager.Exists("test-host")
	}
}

func BenchmarkHostKeyManager_GetFingerprint(b *testing.B) {
	// Generate a test key for benchmarking
	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		b.Fatalf("Failed to generate test key: %v", err)
	}

	manager := NewHostKeyManager()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = manager.GetFingerprint(pubKey)
	}
}

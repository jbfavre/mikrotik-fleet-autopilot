package core

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
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

	// Read test key from testdata
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "test_key.pub")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Skipf("Test key not found: %v", err)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(keyData)
	if err != nil {
		t.Fatalf("Failed to parse test key: %v", err)
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

	// Read test key
	testdataDir, err := filepath.Abs(filepath.Join(originalWd, "testdata"))
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "test_key.pub")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Skipf("Test key not found: %v", err)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(keyData)
	if err != nil {
		t.Fatalf("Failed to parse test key: %v", err)
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

	// Read test key
	testdataDir, err := filepath.Abs(filepath.Join(originalWd, "testdata"))
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "test_key.pub")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Skipf("Test key not found: %v", err)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(keyData)
	if err != nil {
		t.Fatalf("Failed to parse test key: %v", err)
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

	// Read test keys
	testdataDir, err := filepath.Abs(filepath.Join(originalWd, "testdata"))
	if err != nil {
		t.Fatalf("Failed to get testdata path: %v", err)
	}

	// Read first key
	keyPath1 := filepath.Join(testdataDir, "ssh_keys", "test_key.pub")
	keyData1, err := os.ReadFile(keyPath1)
	if err != nil {
		t.Skipf("Test key not found: %v", err)
	}

	pubKey1, _, _, _, err := ssh.ParseAuthorizedKey(keyData1)
	if err != nil {
		t.Fatalf("Failed to parse test key 1: %v", err)
	}

	// Read second key (encrypted key's public part)
	keyPath2 := filepath.Join(testdataDir, "ssh_keys", "encrypted_key.pub")
	keyData2, err := os.ReadFile(keyPath2)
	if err != nil {
		t.Skipf("Test key 2 not found: %v", err)
	}

	pubKey2, _, _, _, err := ssh.ParseAuthorizedKey(keyData2)
	if err != nil {
		t.Fatalf("Failed to parse test key 2: %v", err)
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
	// Read test key
	testdataDir, err := filepath.Abs("testdata")
	if err != nil {
		b.Fatalf("Failed to get testdata path: %v", err)
	}

	keyPath := filepath.Join(testdataDir, "ssh_keys", "test_key.pub")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		b.Skipf("Test key not found: %v", err)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(keyData)
	if err != nil {
		b.Fatalf("Failed to parse test key: %v", err)
	}

	manager := NewHostKeyManager()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = manager.GetFingerprint(pubKey)
	}
}

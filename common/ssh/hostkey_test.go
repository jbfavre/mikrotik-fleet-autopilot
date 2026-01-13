package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBuildHostKeyCallback(t *testing.T) {
	// Generate a test SSH key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}

	tests := []struct {
		name           string
		host           string
		existsFunc     func(host string) bool
		verifyFunc     func(host string, key ssh.PublicKey) error
		captureFunc    func(host string, key ssh.PublicKey) error
		enrollmentMode bool
		wantErr        bool
		errContains    string
	}{
		{
			name: "existing host key - verification succeeds",
			host: "192.168.1.1",
			existsFunc: func(host string) bool {
				return true
			},
			verifyFunc: func(host string, key ssh.PublicKey) error {
				return nil
			},
			enrollmentMode: false,
			wantErr:        false,
		},
		{
			name: "existing host key - verification fails",
			host: "192.168.1.2",
			existsFunc: func(host string) bool {
				return true
			},
			verifyFunc: func(host string, key ssh.PublicKey) error {
				return errors.New("host key verification failed")
			},
			enrollmentMode: false,
			wantErr:        true,
			errContains:    "host key verification failed",
		},
		{
			name: "new host - enrollment mode - capture succeeds",
			host: "192.168.1.3",
			existsFunc: func(host string) bool {
				return false
			},
			captureFunc: func(host string, key ssh.PublicKey) error {
				return nil
			},
			enrollmentMode: true,
			wantErr:        false,
		},
		{
			name: "new host - enrollment mode - capture fails",
			host: "192.168.1.4",
			existsFunc: func(host string) bool {
				return false
			},
			captureFunc: func(host string, key ssh.PublicKey) error {
				return errors.New("failed to capture host key")
			},
			enrollmentMode: true,
			wantErr:        true,
			errContains:    "failed to capture host key",
		},
		{
			name: "new host - not enrollment mode",
			host: "192.168.1.5",
			existsFunc: func(host string) bool {
				return false
			},
			enrollmentMode: false,
			wantErr:        true,
			errContains:    "no host key found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &mockTestHostKeyManager{
				existsFunc:  tt.existsFunc,
				verifyFunc:  tt.verifyFunc,
				captureFunc: tt.captureFunc,
			}

			// Create context with or without enrollment mode
			ctx := context.Background()
			if tt.enrollmentMode {
				// nolint:staticcheck // Using string key to match hostkey.go implementation
				ctx = context.WithValue(ctx, "enrollment", true)
			}

			callback := BuildHostKeyCallback(ctx, tt.host, manager)

			// Call the callback
			err := callback(tt.host, nil, publicKey)

			if tt.wantErr {
				if err == nil {
					t.Error("BuildHostKeyCallback() expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("BuildHostKeyCallback() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("BuildHostKeyCallback() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestBuildHostKeyCallback_HostnameHandling(t *testing.T) {
	// Generate test key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}

	tests := []struct {
		name            string
		buildHost       string // Host used in BuildHostKeyCallback
		callbackHost    string // Host passed to the callback
		expectedHostKey string // Expected host to be passed to manager methods
	}{
		{
			name:            "hostname matches",
			buildHost:       "192.168.1.1",
			callbackHost:    "192.168.1.1",
			expectedHostKey: "192.168.1.1",
		},
		{
			name:            "hostname with port",
			buildHost:       "192.168.1.2",
			callbackHost:    "192.168.1.2:22",
			expectedHostKey: "192.168.1.2",
		},
		{
			name:            "FQDN",
			buildHost:       "router.example.com",
			callbackHost:    "router.example.com",
			expectedHostKey: "router.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedHost string
			manager := &mockTestHostKeyManager{
				existsFunc: func(host string) bool {
					capturedHost = host
					return true
				},
				verifyFunc: func(host string, key ssh.PublicKey) error {
					if host != capturedHost {
						t.Errorf("verify called with different host: %s vs %s", host, capturedHost)
					}
					return nil
				},
			}

			ctx := context.Background()
			callback := BuildHostKeyCallback(ctx, tt.buildHost, manager)

			err := callback(tt.callbackHost, nil, publicKey)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if capturedHost != tt.expectedHostKey {
				t.Errorf("expected host %q to be passed to manager, got %q", tt.expectedHostKey, capturedHost)
			}
		})
	}
}

func TestBuildHostKeyCallback_EnrollmentModeDetection(t *testing.T) {
	// Generate test key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}

	tests := []struct {
		name          string
		setupCtx      func() context.Context
		expectCapture bool
	}{
		{
			name: "enrollment mode with boolean true",
			setupCtx: func() context.Context {
				// nolint:staticcheck // Using string key to match hostkey.go implementation
				return context.WithValue(context.Background(), "enrollment", true)
			},
			expectCapture: true,
		},
		{
			name: "enrollment mode with boolean false",
			setupCtx: func() context.Context {
				// nolint:staticcheck // Using string key to match hostkey.go implementation
				return context.WithValue(context.Background(), "enrollment", false)
			},
			expectCapture: false,
		},
		{
			name: "no enrollment mode in context",
			setupCtx: func() context.Context {
				return context.Background()
			},
			expectCapture: false,
		},
		{
			name: "enrollment mode with wrong type",
			setupCtx: func() context.Context {
				// nolint:staticcheck // Using string key to match hostkey.go implementation
				return context.WithValue(context.Background(), "enrollment", "true")
			},
			expectCapture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captureCalled := false
			manager := &mockTestHostKeyManager{
				existsFunc: func(host string) bool {
					return false // Host key doesn't exist
				},
				captureFunc: func(host string, key ssh.PublicKey) error {
					captureCalled = true
					return nil
				},
			}

			ctx := tt.setupCtx()
			callback := BuildHostKeyCallback(ctx, "192.168.1.1", manager)

			// Call callback - will either capture or error
			_ = callback("192.168.1.1", nil, publicKey)

			if captureCalled != tt.expectCapture {
				t.Errorf("capture called = %v, want %v", captureCalled, tt.expectCapture)
			}
		})
	}
}

func TestBuildHostKeyCallback_ManagerMethodCalls(t *testing.T) {
	// Generate test key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}

	t.Run("Exists is called first", func(t *testing.T) {
		existsCalled := false
		verifyCalled := false

		manager := &mockTestHostKeyManager{
			existsFunc: func(host string) bool {
				existsCalled = true
				if verifyCalled {
					t.Error("Verify was called before Exists")
				}
				return true
			},
			verifyFunc: func(host string, key ssh.PublicKey) error {
				verifyCalled = true
				if !existsCalled {
					t.Error("Exists was not called before Verify")
				}
				return nil
			},
		}

		ctx := context.Background()
		callback := BuildHostKeyCallback(ctx, "192.168.1.1", manager)
		_ = callback("192.168.1.1", nil, publicKey)

		if !existsCalled {
			t.Error("Exists was not called")
		}
		if !verifyCalled {
			t.Error("Verify was not called")
		}
	})

	t.Run("Capture is called when host key doesn't exist in enrollment mode", func(t *testing.T) {
		captureCalled := false
		manager := &mockTestHostKeyManager{
			existsFunc: func(host string) bool {
				return false
			},
			captureFunc: func(host string, key ssh.PublicKey) error {
				captureCalled = true
				return nil
			},
		}

		// nolint:staticcheck // Using string key to match hostkey.go implementation
		ctx := context.WithValue(context.Background(), "enrollment", true)
		callback := BuildHostKeyCallback(ctx, "192.168.1.1", manager)
		_ = callback("192.168.1.1", nil, publicKey)

		if !captureCalled {
			t.Error("Capture was not called in enrollment mode")
		}
	})
}

// Benchmark BuildHostKeyCallback
func BenchmarkBuildHostKeyCallback(b *testing.B) {
	manager := &mockTestHostKeyManager{
		existsFunc: func(host string) bool {
			return true
		},
		verifyFunc: func(host string, key ssh.PublicKey) error {
			return nil
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildHostKeyCallback(ctx, "192.168.1.1", manager)
	}
}

func BenchmarkHostKeyCallback_Execution(b *testing.B) {
	// Generate test key
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	publicKey, _ := ssh.NewPublicKey(&privateKey.PublicKey)

	manager := &mockTestHostKeyManager{
		existsFunc: func(host string) bool {
			return true
		},
		verifyFunc: func(host string, key ssh.PublicKey) error {
			return nil
		},
	}

	ctx := context.Background()
	callback := BuildHostKeyCallback(ctx, "192.168.1.1", manager)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = callback("192.168.1.1", nil, publicKey)
	}
}

func TestBuild_HostKeyRejection(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	ctx := context.Background()
	// Reject all host keys
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return fmt.Errorf("host key rejected")
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)
	_, err := buildTestConnection(ctx, address, "admin", "testpass", "", hostKeyCallback)
	if err == nil {
		t.Error("Build() expected error for rejected host key, got nil")
	}

	if err != nil && !containsAny(err.Error(), []string{"host key", "handshake"}) {
		t.Logf("Build() error = %q", err.Error())
	}
}

func TestBuildHostKeyCallback_SkipVerification(t *testing.T) {
	// Generate test key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}

	tests := []struct {
		name              string
		skipHostKeyCheck  bool
		hostKeyExists     bool
		enrollmentMode    bool
		expectCaptureCall bool
	}{
		{
			name:              "skip verification - host key exists",
			skipHostKeyCheck:  true,
			hostKeyExists:     true,
			enrollmentMode:    false,
			expectCaptureCall: false,
		},
		{
			name:              "skip verification - new host in enrollment",
			skipHostKeyCheck:  true,
			hostKeyExists:     false,
			enrollmentMode:    true,
			expectCaptureCall: true,
		},
		{
			name:              "skip verification - new host not in enrollment",
			skipHostKeyCheck:  true,
			hostKeyExists:     false,
			enrollmentMode:    false,
			expectCaptureCall: false,
		},
		{
			name:              "verification enabled - existing host",
			skipHostKeyCheck:  false,
			hostKeyExists:     true,
			enrollmentMode:    false,
			expectCaptureCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captureCalled := false
			manager := &mockTestHostKeyManager{
				existsFunc: func(host string) bool {
					return tt.hostKeyExists
				},
				verifyFunc: func(host string, key ssh.PublicKey) error {
					return nil
				},
				captureFunc: func(host string, key ssh.PublicKey) error {
					captureCalled = true
					return nil
				},
				getFingerprintFunc: func(key ssh.PublicKey) string {
					return "SHA256:test"
				},
			}

			// Build context with config
			ctx := context.Background()
			if tt.skipHostKeyCheck {
				// nolint:staticcheck // Using string key to match hostkey.go implementation
				ctx = context.WithValue(ctx, "config", &testConfigSkipCheck{})
			}
			if tt.enrollmentMode {
				// nolint:staticcheck
				ctx = context.WithValue(ctx, "enrollment", true)
			}

			callback := BuildHostKeyCallback(ctx, "192.168.1.1", manager)
			err := callback("192.168.1.1", nil, publicKey)

			if err != nil {
				t.Errorf("BuildHostKeyCallback() unexpected error = %v", err)
			}
			if captureCalled != tt.expectCaptureCall {
				t.Errorf("BuildHostKeyCallback() capture called = %v, want %v", captureCalled, tt.expectCaptureCall)
			}
		})
	}
}
// Tests for DefaultHostKeyManager

func TestNewDefaultHostKeyManager(t *testing.T) {
	manager := NewDefaultHostKeyManager()
	if manager == nil {
		t.Fatal("NewDefaultHostKeyManager() returned nil")
	}
}

func TestDefaultHostKeyManager_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewDefaultHostKeyManager()

	if manager.Exists("nonexistent-host") {
		t.Error("Exists() should return false for nonexistent host key")
	}

	testFile := "test-host.hostkey"
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if !manager.Exists("test-host") {
		t.Error("Exists() should return true for existing host key")
	}
}

func TestDefaultHostKeyManager_GetFingerprint(t *testing.T) {
	manager := NewDefaultHostKeyManager()

	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	fingerprint := manager.GetFingerprint(pubKey)
	if fingerprint == "" {
		t.Error("GetFingerprint() returned empty string for valid key")
	}

	if len(fingerprint) < 10 {
		t.Errorf("GetFingerprint() returned unexpectedly short fingerprint: %s", fingerprint)
	}
}

func TestDefaultHostKeyManager_Capture(t *testing.T) {
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewDefaultHostKeyManager()

	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "test-capture-host"
	if err := manager.Capture(host, pubKey); err != nil {
		t.Errorf("Capture() failed: %v", err)
	}

	expectedFile := host + ".hostkey"
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Errorf("Capture() did not create expected file: %s", expectedFile)
	}

	if !manager.Exists(host) {
		t.Error("Exists() should return true after Capture()")
	}
}

func TestDefaultHostKeyManager_Verify(t *testing.T) {
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewDefaultHostKeyManager()

	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "test-verify-host"

	if err := manager.Capture(host, pubKey); err != nil {
		t.Fatalf("Capture() failed: %v", err)
	}

	if err := manager.Verify(host, pubKey); err != nil {
		t.Errorf("Verify() failed with correct key: %v", err)
	}
}

func TestDefaultHostKeyManager_VerifyWrongKey(t *testing.T) {
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()
	_ = os.Chdir(tmpDir)

	manager := NewDefaultHostKeyManager()

	pubKey1, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}
	pubKey2, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "test-verify-wrong-key"

	if err := manager.Capture(host, pubKey1); err != nil {
		t.Fatalf("Capture() failed: %v", err)
	}

	if err := manager.Verify(host, pubKey2); err == nil {
		t.Error("Verify() should fail with wrong key")
	}
}

func BenchmarkDefaultHostKeyManager_Exists(b *testing.B) {
	manager := NewDefaultHostKeyManager()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = manager.Exists("test-host")
	}
}

func BenchmarkDefaultHostKeyManager_GetFingerprint(b *testing.B) {
	pubKey, err := generateTestKeyEd25519()
	if err != nil {
		b.Fatalf("Failed to generate test key: %v", err)
	}

	manager := NewDefaultHostKeyManager()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = manager.GetFingerprint(pubKey)
	}
}

// Tests for hostkey functions

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

func TestGetHostKeyFingerprint_Direct(t *testing.T) {
	key, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	fp := GetHostKeyFingerprint(key)

	if len(fp) < 8 || fp[:7] != "SHA256:" {
		t.Errorf("GetHostKeyFingerprint() = %s, expected format SHA256:...", fp)
	}

	fp2 := GetHostKeyFingerprint(key)
	if fp != fp2 {
		t.Errorf("GetHostKeyFingerprint() not consistent: %s != %s", fp, fp2)
	}
}

func TestCaptureAndLoadHostKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Logf("Failed to restore directory: %v", err)
		}
	}()

	testKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	host := "testrouter"

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	if !HostKeyExists(host) {
		t.Error("HostKeyExists() = false after capture, want true")
	}

	loadedKey, err := LoadHostKey(host)
	if err != nil {
		t.Fatalf("LoadHostKey() failed: %v", err)
	}

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

func TestVerifyHostKey_Direct(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Logf("Failed to restore directory: %v", err)
		}
	}()

	correctKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	wrongKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate wrong key: %v", err)
	}

	host := "testrouter"

	if err := CaptureHostKey(host, correctKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	if err := VerifyHostKey(host, correctKey); err != nil {
		t.Errorf("VerifyHostKey() with correct key failed: %v", err)
	}

	if err := VerifyHostKey(host, wrongKey); err == nil {
		t.Error("VerifyHostKey() with wrong key succeeded, want error")
	}
}

func TestHostKeyExists_Direct(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

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

	if HostKeyExists(host) {
		t.Error("HostKeyExists() = true before creation, want false")
	}

	testKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	if !HostKeyExists(host) {
		t.Error("HostKeyExists() = false after creation, want true")
	}
}

func TestDeleteHostKey_Direct(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	if err := DeleteHostKey(host); err == nil {
		t.Error("DeleteHostKey() on non-existent key succeeded, want error")
	}

	testKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	if err := DeleteHostKey(host); err != nil {
		t.Errorf("DeleteHostKey() failed: %v", err)
	}

	if HostKeyExists(host) {
		t.Error("HostKeyExists() = true after deletion, want false")
	}
}

func TestLoadHostKeyInfo_Direct(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	testKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	info, err := LoadHostKeyInfo(host)
	if err != nil {
		t.Fatalf("LoadHostKeyInfo() failed: %v", err)
	}

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

	if time.Since(info.CapturedAt) > time.Minute {
		t.Errorf("CapturedAt = %v, seems too old", info.CapturedAt)
	}
}

func TestHostKeyFileFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	testKey, err := generateTestKeyEd25519()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	if err := CaptureHostKey(host, testKey); err != nil {
		t.Fatalf("CaptureHostKey() failed: %v", err)
	}

	data, err := os.ReadFile(HostKeyFilePath(host))
	if err != nil {
		t.Fatalf("Failed to read host key file: %v", err)
	}

	var info HostKeyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("Failed to unmarshal host key file: %v", err)
	}

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
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

	path := HostKeyFilePath(host)
	if err := os.WriteFile(path, []byte("invalid json"), 0600); err != nil {
		t.Fatalf("Failed to write invalid file: %v", err)
	}

	if _, err := LoadHostKey(host); err == nil {
		t.Error("LoadHostKey() with invalid JSON succeeded, want error")
	}
}

func TestLoadHostKeyCorruptedKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hostkey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	originalDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	host := "testrouter"

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

	if _, err := LoadHostKey(host); err == nil {
		t.Error("LoadHostKey() with invalid base64 succeeded, want error")
	}
}
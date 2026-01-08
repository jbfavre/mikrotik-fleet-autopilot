package ssh

import (
	"fmt"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"

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
				return context.WithValue(context.Background(), "enrollment", true)
			},
			expectCapture: true,
		},
		{
			name: "enrollment mode with boolean false",
			setupCtx: func() context.Context {
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

		type enrollmentKey string
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

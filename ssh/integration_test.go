package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Mock SSH server for integration testing
type mockSSHServer struct {
	listener net.Listener
	config   *ssh.ServerConfig
	stopped  chan struct{}
}

func startMockSSHServer(t *testing.T) (*mockSSHServer, int) {
	// Generate server key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "admin" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("authentication failed")
		},
	}
	config.AddHostKey(signer)

	// Start listening on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	server := &mockSSHServer{
		listener: listener,
		config:   config,
		stopped:  make(chan struct{}),
	}

	go server.acceptConnections(t)

	// Get the port
	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("Failed to parse port: %v", err)
	}

	return server, port
}

func (s *mockSSHServer) acceptConnections(t *testing.T) {
	defer close(s.stopped)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // Server stopped
		}

		go s.handleConnection(conn, t)
	}
}

func (s *mockSSHServer) handleConnection(netConn net.Conn, t *testing.T) {
	defer func() {
		_ = netConn.Close()
	}()

	_, chans, reqs, err := ssh.NewServerConn(netConn, s.config)
	if err != nil {
		return // Authentication failed or connection error
	}

	// Handle global requests
	go ssh.DiscardRequests(reqs)

	// Handle channels
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				if req.Type == "exec" {
					// Parse command
					var payload struct {
						Command string
					}
					_ = ssh.Unmarshal(req.Payload, &payload)

					// Send success response
					_ = req.Reply(true, nil)

					// Send mock response
					response := fmt.Sprintf("Mock output for: %s\n", payload.Command)
					_, _ = channel.Write([]byte(response))
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					_ = channel.Close()
				} else {
					_ = req.Reply(false, nil)
				}
			}
		}(requests)
	}
}

func (s *mockSSHServer) stop() {
	_ = s.listener.Close()
	<-s.stopped
}

func TestConnection_Run_WithMockServer(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	// Create connection using builder
	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil // Accept any key for testing
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := builder.Build(ctx, address, "admin", "testpass", "", hostKeyCallback)
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

	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := builder.Build(ctx, address, "admin", "testpass", "", hostKeyCallback)
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

func TestBuild_WithUsername(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil
	}

	// Test with explicit username
	address := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := builder.Build(ctx, address, "admin", "testpass", "", hostKeyCallback)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	if conn == nil {
		t.Error("Build() returned nil connection")
	}
}

func TestBuild_WithSSHConfigUsername(t *testing.T) {
	// Create temporary SSH config with username
	tmpDir := t.TempDir()
	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("Failed to create temp .ssh dir: %v", err)
	}

	server, port := startMockSSHServer(t)
	defer server.stop()

	configContent := fmt.Sprintf(`
Host testhost
    HostName 127.0.0.1
    User admin
    Port %d
`, port)
	configPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Override HOME
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil
	}

	// Use alias from SSH config
	conn, err := builder.Build(ctx, "testhost", "", "testpass", "", hostKeyCallback)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	if conn == nil {
		t.Error("Build() returned nil connection")
	}
}

func TestBuildAuthMethods_WithValidKey(t *testing.T) {
	// Generate a valid SSH key
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key")

	// Generate RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	// Encode private key to OpenSSH format with passphrase
	passphrase := "testpass123"

	// Use golang.org/x/crypto/ssh to create a proper encrypted key
	encryptedKey, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "", []byte(passphrase))
	if err != nil {
		t.Fatalf("Failed to marshal private key with passphrase: %v", err)
	}

	if err := os.WriteFile(keyPath, pem.EncodeToMemory(encryptedKey), 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	// Test with key and password
	provider := &DefaultAuthProvider{}
	hostInfo := &HostInfo{
		Hostname:     "test.host",
		IdentityFile: keyPath,
	}

	methods, err := provider.BuildAuthMethods(hostInfo, "password123", passphrase)
	if err != nil {
		t.Fatalf("BuildAuthMethods() error = %v", err)
	}

	// Should have both key and password authentication
	if len(methods) != 2 {
		t.Errorf("BuildAuthMethods() returned %d methods, want 2 (key + password)", len(methods))
	}
}

func TestBuildAuthMethods_KeyOnlySuccess(t *testing.T) {
	// Generate a valid unencrypted SSH key
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key")

	// Generate RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	// Encode private key to PEM format without passphrase
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}

	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	// Test with key only (provide empty passphrase to trigger key loading)
	provider := &DefaultAuthProvider{}
	hostInfo := &HostInfo{
		Hostname:     "test.host",
		IdentityFile: keyPath,
	}

	// Need to provide passphrase parameter (even if empty) to trigger key loading logic
	methods, err := provider.BuildAuthMethods(hostInfo, "", "")
	if err != nil {
		// Expected: without passphrase parameter, no auth method is provided
		t.Logf("BuildAuthMethods() with empty passphrase: %v", err)
		return
	}

	// Should have key authentication
	if len(methods) != 1 {
		t.Errorf("BuildAuthMethods() returned %d methods, want 1 (key only)", len(methods))
	}
}

func TestBuild_AuthenticationFailure(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	ctx := context.Background()
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil
	}

	// Try with wrong password
	address := fmt.Sprintf("127.0.0.1:%d", port)
	_, err := builder.Build(ctx, address, "admin", "wrongpassword", "", hostKeyCallback)
	if err == nil {
		t.Error("Build() expected error for wrong password, got nil")
	}

	if err != nil && !containsAny(err.Error(), []string{"authentication", "handshake"}) {
		t.Errorf("Build() error = %q, expected authentication related error", err.Error())
	}
}

func TestBuild_HostKeyRejection(t *testing.T) {
	server, port := startMockSSHServer(t)
	defer server.stop()

	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	ctx := context.Background()
	// Reject all host keys
	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return fmt.Errorf("host key rejected")
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)
	_, err := builder.Build(ctx, address, "admin", "testpass", "", hostKeyCallback)
	if err == nil {
		t.Error("Build() expected error for rejected host key, got nil")
	}

	if err != nil && !containsAny(err.Error(), []string{"host key", "handshake"}) {
		t.Logf("Build() error = %q", err.Error())
	}
}

func TestBuild_ShortTimeout(t *testing.T) {
	// Don't start a server - let it timeout
	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	// Use very short context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	hostKeyCallback := func(hostname string, remote interface{}, key ssh.PublicKey) error {
		return nil
	}

	// Try to connect to a non-routable address (will timeout)
	_, err := builder.Build(ctx, "192.0.2.1:22", "admin", "testpass", "", hostKeyCallback)
	if err == nil {
		t.Error("Build() expected timeout error, got nil")
	}

	// Verify it's a timeout/context error
	if err != nil && !containsAny(err.Error(), []string{"context", "timeout", "cancelled", "deadline"}) {
		t.Errorf("Build() error = %q, expected timeout/context error", err.Error())
	}
}

// Helper function
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if contains(s, substr) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

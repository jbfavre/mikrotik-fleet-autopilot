package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// mockSSHServer is a mock SSH server for testing SSH connections
type mockSSHServer struct {
	listener net.Listener
	config   *ssh.ServerConfig
	stopped  chan struct{}
}

// startMockSSHServer creates and starts a mock SSH server on a random port
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

// buildTestConnection is a helper function for tests to build SSH connections
// with explicit control over parameters. It provides low-level access for testing
// connection establishment with specific configurations.
func buildTestConnection(ctx context.Context, host, username, password, passphrase string, hostKeyCallback HostKeyCallback) (Runner, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before connection: %w", err)
	}

	// Read SSH configuration
	hostInfo, err := readConfig(host)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH config: %w", err)
	}

	// Build authentication methods
	authMethods, err := buildAuthMethods(hostInfo, password, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to build auth methods: %w", err)
	}

	// Determine which username to use
	finalUsername := username
	if hostInfo.User != "" {
		finalUsername = hostInfo.User
	}

	// Build SSH client config
	config := &ssh.ClientConfig{
		User: finalUsername,
		Auth: authMethods,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return hostKeyCallback(hostname, remote, key)
		},
		Timeout: 10 * time.Second,
	}

	// Establish connection with context awareness
	address := net.JoinHostPort(hostInfo.Hostname, hostInfo.Port)

	// Check context before dialing
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before dial: %w", err)
	}

	// Create a dialer that respects context
	dialer := net.Dialer{
		Timeout: config.Timeout,
	}
	netConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	// Perform SSH handshake
	c, chans, reqs, err := ssh.NewClientConn(netConn, address, config)
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("SSH handshake failed for %s: %w", address, err)
	}

	client := ssh.NewClient(c, chans, reqs)
	return &DefaultRunner{client: client}, nil
}

// containsAny checks if string s contains any of the substrings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if contains(s, substr) {
			return true
		}
	}
	return false
}

// contains checks if string s contains substr (case-sensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

// findSubstring searches for substr in s
func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

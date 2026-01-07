package ssh

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultConnectionBuilder implements ConnectionBuilder
type DefaultConnectionBuilder struct {
	configReader ConfigReader
	authProvider AuthProvider
}

// NewConnectionBuilder creates a new ConnectionBuilder
func NewConnectionBuilder(configReader ConfigReader, authProvider AuthProvider) *DefaultConnectionBuilder {
	return &DefaultConnectionBuilder{
		configReader: configReader,
		authProvider: authProvider,
	}
}

// Build creates a new SSH connection
func (b *DefaultConnectionBuilder) Build(ctx context.Context, host, username, password, passphrase string, hostKeyCallback HostKeyCallback) (Runner, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before connection: %w", err)
	}

	// Step 1: Read SSH configuration
	hostInfo, err := b.configReader.ReadConfig(host)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH config: %w", err)
	}

	slog.Debug("SSH host configuration",
		"original", hostInfo.Original,
		"type", hostInfo.Type,
		"hostname", hostInfo.Hostname,
		"port", hostInfo.Port,
		"user", hostInfo.User,
		"identityFile", hostInfo.IdentityFile)

	// Step 2: Build authentication methods
	authMethods, err := b.authProvider.BuildAuthMethods(hostInfo, password, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to build auth methods: %w", err)
	}

	// Step 3: Determine which username to use
	finalUsername := username
	if hostInfo.User != "" {
		slog.Debug("using username from ssh_config", "user", hostInfo.User)
		finalUsername = hostInfo.User
	} else {
		slog.Debug("using username from command line", "user", username)
	}

	// Step 4: Build SSH client config
	config := &ssh.ClientConfig{
		User: finalUsername,
		Auth: authMethods,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return hostKeyCallback(hostname, remote, key)
		},
		Timeout: 10 * time.Second,
	}

	// Step 5: Establish connection with context awareness
	address := net.JoinHostPort(hostInfo.Hostname, hostInfo.Port)
	slog.Debug("establishing SSH connection", "address", address)

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
		slog.Error("failed to dial", "address", address, "error", err)
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	// Perform SSH handshake
	c, chans, reqs, err := ssh.NewClientConn(netConn, address, config)
	if err != nil {
		_ = netConn.Close() // Ignore close error when handshake failed
		slog.Error("SSH handshake failed", "address", address, "error", err)
		return nil, fmt.Errorf("SSH handshake failed for %s: %w", address, err)
	}

	client := ssh.NewClient(c, chans, reqs)

	slog.Debug("SSH connection established")
	return &Connection{client: client}, nil
}

// Connection implements Runner interface
type Connection struct {
	client *ssh.Client
}

// GetClient returns the underlying SSH client (for backward compatibility)
func (c *Connection) GetClient() *ssh.Client {
	return c.client
}

func (c *Connection) Close() error {
	if c.client == nil {
		return nil
	}
	err := c.client.Close()
	if err != nil && !c.IsAlreadyClosedError(err) {
		slog.Warn("failed to close SSH connection", "error", err)
		return err
	}
	return nil
}

func (c *Connection) IsAlreadyClosedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "use of closed network connection") ||
		strings.Contains(errMsg, "connection already closed")
}

func (c *Connection) Run(cmd string) (string, error) {
	if c.client == nil {
		slog.Warn("SSH connection not established")
		return "", fmt.Errorf("SSH connection not established")
	}

	session, err := c.client.NewSession()
	if err != nil {
		slog.Warn("failed to create session", "error", err)
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	var b bytes.Buffer
	session.Stdout = &b
	if err := session.Run(cmd); err != nil {
		slog.Warn("failed to run command", "command", cmd, "error", err)
		return "", fmt.Errorf("failed to run command: %v", err)
	}
	return b.String(), nil
}

package ssh

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultConnectionBuilder implements ConnectionBuilder
type DefaultConnectionBuilder struct {
	configReader ConfigReader
}

// NewConnectionBuilder creates a new ConnectionBuilder
func NewConnectionBuilder(configReader ConfigReader) *DefaultConnectionBuilder {
	return &DefaultConnectionBuilder{
		configReader: configReader,
	}
}

// Build creates a new SSH connection
func (b *DefaultConnectionBuilder) Build(ctx context.Context, host, username, password, passphrase string, hostKeyCallback HostKeyCallback) (Runner, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before connection: %w", err)
	}

	// Step 1: Read SSH configuration
	hostInfo, err := readConfig(host)
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
	authMethods, err := buildAuthMethods(hostInfo, password, passphrase)
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
	return &DefaultRunner{client: client}, nil
}

package core

import (
	"context"
	"fmt"
	"log/slog"
)

// SshManager encapsulates SSH credentials and provides SSH connections
// without exposing credentials to callers
type SshManager struct {
	user       string
	password   string
	passphrase string
}

// NewSshManager creates a new SSH manager with the provided credentials
// Credentials are stored privately and never exposed outside this package
func NewSshManager(user, password, passphrase string) *SshManager {
	return &SshManager{
		user:       user,
		password:   password,
		passphrase: passphrase,
	}
}

// CreateConnection retrieves the SshManager from context and creates a new SSH connection
// to the specified host (automatically appending :22 port if not present).
// This is the standard way to create SSH connections in subcommands.
func CreateConnection(ctx context.Context, host string) (SshRunner, error) {
	manager, err := GetSshManager(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH manager from context: %w", err)
	}

	// Call the internal newSsh function
	slog.Debug("creating SSH connection", "host", host, "user", manager.user)
	conn, err := newSsh(host, manager.user, manager.password, manager.passphrase)
	if err != nil {
		slog.Error("failed to create SSH connection", "host", host, "error", err)
		return nil, fmt.Errorf("failed to create SSH connection to %s: %w", host, err)
	}

	return conn, nil
}

// GetUser returns the username (non-sensitive information)
// This can be useful for logging purposes
func (m *SshManager) GetUser() string {
	return m.user
}

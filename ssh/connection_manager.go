package ssh

import (
	"context"
	"fmt"
	"log/slog"
)

// CreateConnection creates a new SSH connection using the provided credentials and host key manager.
// This is the main entry point for creating SSH connections in the application.
func CreateConnection(ctx context.Context, host string, credentials CredentialsProvider, hostKeyManager HostKeyManager) (Runner, error) {
	slog.Debug("creating SSH connection", "host", host, "user", credentials.GetUser())

	// Create the connection builder
	configReader := &DefaultConfigReader{}
	authProvider := &DefaultAuthProvider{}
	builder := NewConnectionBuilder(configReader, authProvider)

	// Create host key callback
	hostKeyCallback := BuildHostKeyCallback(ctx, host, hostKeyManager)

	// Build the connection
	conn, err := builder.Build(ctx, host, credentials.GetUser(), credentials.GetPassword(), credentials.GetPassphrase(), hostKeyCallback)
	if err != nil {
		slog.Error("failed to create SSH connection", "host", host, "error", err)
		return nil, fmt.Errorf("failed to create SSH connection to %s: %w", host, err)
	}

	return conn, nil
}

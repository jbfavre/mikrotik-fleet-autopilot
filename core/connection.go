package core

import (
	"context"
	"fmt"

	sshpkg "jb.favre/mikrotik-fleet-autopilot/ssh"
)

// CreateConnection retrieves credentials and host key manager from context
// and creates a new SSH connection to the specified host.
// This is the standard way to create SSH connections in subcommands.
func CreateConnection(ctx context.Context, host string) (sshpkg.Runner, error) {
	// Get credentials provider
	credentials, err := GetSshCredentialsProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials from context: %w", err)
	}

	// Get host key manager
	hostKeyManager := GetHostKeyManager()

	// Delegate to ssh package
	return sshpkg.CreateConnection(ctx, host, credentials, hostKeyManager)
}

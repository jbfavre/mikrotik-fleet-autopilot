package ssh

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DefaultRunner implements Runner interface
type DefaultRunner struct {
	client *ssh.Client
}

// GetClient returns the underlying SSH client (for backward compatibility)
func (r *DefaultRunner) GetClient() *ssh.Client {
	return r.client
}

func (r *DefaultRunner) Close() error {
	if r.client == nil {
		return nil
	}
	err := r.client.Close()
	if err != nil && !r.IsAlreadyClosedError(err) {
		slog.Warn("failed to close SSH connection", "error", err)
		return err
	}
	return nil
}

func (r *DefaultRunner) IsAlreadyClosedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "use of closed network connection") ||
		strings.Contains(errMsg, "connection already closed")
}

func (r *DefaultRunner) Run(cmd string) (string, error) {
	if r.client == nil {
		slog.Warn("SSH connection not established")
		return "", fmt.Errorf("SSH connection not established")
	}

	session, err := r.client.NewSession()
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

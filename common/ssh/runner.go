package ssh

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"golang.org/x/crypto/ssh"
)

// RunnerInterface defines the methods for SSH operations
type RunnerInterface interface {
	Close() error
	IsAlreadyClosedError(err error) bool
	Run(cmd string) (string, error)
	RunInteractive(input string) (string, error)
}

// Runner implements RunnerInterface using an SSH client
type Runner struct {
	client *ssh.Client
}

func (r *Runner) Close() error {
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

func (r *Runner) IsAlreadyClosedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "use of closed network connection") ||
		strings.Contains(errMsg, "connection already closed")
}

func (r *Runner) Run(cmd string) (string, error) {
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

func (r *Runner) RunInteractive(input string) (string, error) {
	if r.client == nil {
		slog.Warn("SSH connection not established")
		return "", fmt.Errorf("SSH connection not established")
	}

	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	var stdout bytes.Buffer
	session.Stdout = &stdout

	if err := session.RequestPty("xterm", 80, 40, ssh.TerminalModes{}); err != nil {
		return "", fmt.Errorf("failed to request PTY: %w", err)
	}

	if err := session.Shell(); err != nil {
		return "", fmt.Errorf("failed to start shell: %w", err)
	}

	if _, err := io.WriteString(stdin, input); err != nil {
		return "", fmt.Errorf("failed to write input: %w", err)
	}
	_ = stdin.Close()

	if err := session.Wait(); err != nil {
		return stdout.String(), fmt.Errorf("interactive session failed: %w", err)
	}

	return stdout.String(), nil
}

// getClient returns the underlying SSH client
func (r *Runner) getClient() *ssh.Client {
	return r.client
}

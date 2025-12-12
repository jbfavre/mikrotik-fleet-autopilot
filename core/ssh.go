package core

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// SshRunner defines the interface for SSH operations
type SshRunner interface {
	Run(cmd string) (string, error)
	Close() error
}

type SshConnection struct {
	client *ssh.Client
}

func NewSsh(host, username, password string) (*SshConnection, error) {
	// var hostKey ssh.PublicKey
	// An SSH client is represented with a ClientConn.
	//
	// To authenticate with the remote server you must pass at least one
	// implementation of AuthMethod via the Auth field in ClientConfig,
	// and provide a HostKeyCallback.
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //ssh.FixedHostKey(hostKey),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial, %v", err)
	}

	conn := &SshConnection{
		client: client,
	}

	return conn, nil
}

func (c *SshConnection) Close() error {
	err := c.client.Close()
	if err != nil && !IsAlreadyClosedError(err) {
		slog.Debug("failed to close SSH connection: " + err.Error())
		return err
	}
	// Silently ignore "already closed" errors as they're expected in some scenarios
	return nil
}

// IsAlreadyClosedError checks if the error is due to closing an already closed connection
func IsAlreadyClosedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "use of closed network connection") ||
		strings.Contains(errMsg, "connection already closed")
}

func (c *SshConnection) Run(cmd string) (string, error) {
	// Each ClientConn can support multiple interactive sessions,
	// represented by a Session.
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session, %v", err)
	}
	defer func() {
		_ = session.Close() // Explicitly ignore close error on session as connection may already be closed
	}()

	// Once a Session is created, you can execute a single command on
	// the remote side using the Run method.
	var b bytes.Buffer
	session.Stdout = &b
	if err := session.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to run, %v", err.Error())
	}
	return b.String(), nil
}

func GetPassword(prompt string) string {
	fmt.Print(prompt)

	// Common settings and variables for both stty calls.
	attrs := syscall.ProcAttr{
		Dir:   "",
		Env:   []string{},
		Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
		Sys:   nil}
	var ws syscall.WaitStatus

	// Disable echoing.
	pid, err := syscall.ForkExec(
		"/bin/stty",
		[]string{"stty", "-echo"},
		&attrs)
	if err != nil {
		panic(err)
	}

	// Wait for the stty process to complete.
	_, err = syscall.Wait4(pid, &ws, 0, nil)
	if err != nil {
		panic(err)
	}

	// Echo is disabled, now grab the data.
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	if err != nil {
		panic(err)
	}

	// Re-enable echo.
	pid, err = syscall.ForkExec(
		"/bin/stty",
		[]string{"stty", "echo"},
		&attrs)
	if err != nil {
		panic(err)
	}

	// Wait for the stty process to complete.
	_, err = syscall.Wait4(pid, &ws, 0, nil)
	if err != nil {
		panic(err)
	}

	return strings.TrimSpace(text)
}

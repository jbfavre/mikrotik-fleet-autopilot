package core

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
)

// SshRunner defines the interface for SSH operations
type SshRunner interface {
	Close() error
	IsAlreadyClosedError(err error) bool
	Run(cmd string) (string, error)
}

type sshConnection struct {
	client       *ssh.Client
	clientConfig *ssh.ClientConfig
}

func (c *sshConnection) Close() error {
	err := c.client.Close()
	if err != nil && !c.IsAlreadyClosedError(err) {
		slog.Debug("failed to close SSH connection: " + err.Error())
		return err
	}
	// Silently ignore "already closed" errors as they're expected in some scenarios
	return nil
}

// IsAlreadyClosedError checks if the error is due to closing an already closed connection
func (c *sshConnection) IsAlreadyClosedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "use of closed network connection") ||
		strings.Contains(errMsg, "connection already closed")
}

func (c *sshConnection) Run(cmd string) (string, error) {
	// Check if connection is established
	if c.client == nil {
		return "", fmt.Errorf("SSH connection not established")
	}

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

// newSsh creates a new SSH connection (internal function, use SshManager.CreateConnection instead)
func newSsh(host, username, password, passphrase string) (*sshConnection, error) {
	// To authenticate with the remote server you must pass at least one
	// implementation of AuthMethod via the Auth field in ClientConfig,
	// and provide a HostKeyCallback.
	conn := &sshConnection{
		client:       nil,
		clientConfig: nil,
	}

	hostConfig := readSshConfig(host)
	slog.Debug("hostConfig Hostname is " + hostConfig["Hostname"])
	slog.Debug("hostConfig User is " + hostConfig["User"])
	slog.Debug("hostConfig IdentityFile is " + hostConfig["IdentityFile"])

	var sshSigner ssh.Signer
	var authMethod []ssh.AuthMethod
	// Try to load SSH key if passphrase is provided
	if passphrase != "" {
		slog.Debug("We have a passphrase, trying to unlock private key")
		var err error
		sshSigner, err = parseSshPrivateKey(hostConfig["IdentityFile"], passphrase)
		if err != nil {
			slog.Debug("failed to parse SSH private key with provided passphrase")
			return nil, err
		}
		if sshSigner == nil {
			slog.Debug("sshSigner is nil")
		}
		slog.Debug("We got a valid " + sshSigner.PublicKey().Type() + " sshSigner")
	}

	// Build authentication methods
	if sshSigner != nil && password != "" {
		slog.Debug("We have both a valid key and a passord")
		// Both key and password available
		authMethod = []ssh.AuthMethod{
			ssh.PublicKeys(sshSigner),
			ssh.Password(password),
		}
	} else if sshSigner != nil {
		slog.Debug("We have a valid key")
		// Only key available
		authMethod = []ssh.AuthMethod{
			ssh.PublicKeys(sshSigner),
		}
	} else if password != "" {
		slog.Debug("We have a password")
		// Only password available
		authMethod = []ssh.AuthMethod{
			ssh.Password(password),
		}
	} else {
		slog.Debug("no authentication method provided (need password or SSH key with passphrase)")
		return nil, fmt.Errorf("no authentication method provided (need password or SSH key with passphrase)")
	}
	slog.Debug("We have everything we need to build the ssh.ClientConfig")
	// Build ssh client config
	config := &ssh.ClientConfig{
		User: username,
		Auth: authMethod,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
		Timeout: 10 * time.Second,
	}
	conn.clientConfig = config

	// Establish the SSH connection
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", host, err)
	}
	conn.client = client

	return conn, nil
}

func readSshConfig(host string) map[string]string {
	// Initialize hostConfig with defaults
	hostConfig := make(map[string]string)
	hostConfig["Hostname"] = host
	hostConfig["Port"] = "22"
	hostConfig["User"] = ""
	hostConfig["IdentityFile"] = ""
	hostConfig["IdentitiesOnly"] = ""
	hostConfig["ForwardAgent"] = ""
	hostConfig["HostkeyAlgorithms"] = ""
	hostConfig["PubkeyAcceptedAlgorithms"] = ""

	// Try to read from user's ssh_config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Could not determine home directory - use defaults
		return hostConfig
	}
	sshConfigFile, err := os.Open(filepath.Join(homeDir, ".ssh", "config"))
	if err != nil {
		// SSH config file doesn't exist or can't be read - use defaults
		return hostConfig
	}
	defer func() { _ = sshConfigFile.Close() }()

	sshConfig, err := ssh_config.Decode(sshConfigFile)
	if err != nil || sshConfig == nil {
		// Failed to decode SSH config - use defaults
		return hostConfig
	}

	// Override host's config values with settings from ssh_config
	if hostname, _ := sshConfig.Get(host, "Hostname"); hostname != "" {
		hostConfig["Hostname"] = strings.ReplaceAll(hostname, "%h", host)
	}
	hostConfig["User"], _ = sshConfig.Get(host, "User")
	hostConfig["IdentitiesOnly"], _ = sshConfig.Get(host, "IdentitiesOnly")
	hostConfig["ForwardAgent"], _ = sshConfig.Get(host, "ForwardAgent")
	hostConfig["HostkeyAlgorithms"], _ = sshConfig.Get(host, "HostkeyAlgorithms")
	hostConfig["PubkeyAcceptedAlgorithms"], _ = sshConfig.Get(host, "PubkeyAcceptedAlgorithms")

	if port, _ := sshConfig.Get(host, "Port"); port != "" && port != "0" {
		hostConfig["Port"] = port
	}
	hostConfig["IdentityFile"], _ = sshConfig.Get(host, "IdentityFile")

	return hostConfig
}

func parseSshPrivateKey(identityFile, passphrase string) (ssh.Signer, error) {
	// Get current user's detail
	user, err := user.Current()
	if err != nil {
		slog.Error("unable to get current user: " + fmt.Sprintf("%v", err.Error()))
		return nil, err
	}
	userHomeDir := user.HomeDir
	// Expand ~/ IdentityFile with full user's home path
	if strings.HasPrefix(identityFile, "~/") {
		identityFile = filepath.Join(userHomeDir, identityFile[2:])
	}

	// If Identity File found, parse private key and add ssh.PublicKeys(signer) to AuthMethod
	// Parse private key and build ssh.signer
	slog.Debug("Trying to read " + identityFile)
	key, err := os.ReadFile(identityFile)
	if err != nil {
		slog.Error("unable to read private key: " + fmt.Sprintf("%v", err.Error()))
		return nil, err
	}
	slog.Debug("We got our key from " + identityFile)

	var signer ssh.Signer
	slog.Debug(("Unlocking private key with provided passphrase"))
	signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	if err != nil {
		slog.Warn("unable to parse private key: " + fmt.Sprintf("%v", err.Error()))
		return nil, err
	}
	slog.Debug("Private key type is " + signer.PublicKey().Type())
	return signer, nil
}

/* func getPassword(prompt string) string {
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
} */

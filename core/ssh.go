package core

import (
	"bytes"
	"context"
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
		slog.Warn("failed to close SSH connection", "error", err)
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
		slog.Warn("SSH connection not established")
		return "", fmt.Errorf("SSH connection not established")
	}

	// Each ClientConn can support multiple interactive sessions,
	// represented by a Session.
	session, err := c.client.NewSession()
	if err != nil {
		slog.Warn("failed to create session", "error", err)
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer func() {
		_ = session.Close() // Explicitly ignore close error on session as connection may already be closed
	}()

	// Once a Session is created, you can execute a single command on
	// the remote side using the Run method.
	var b bytes.Buffer
	session.Stdout = &b
	if err := session.Run(cmd); err != nil {
		slog.Warn("failed to run command", "command", cmd, "error", err)
		return "", fmt.Errorf("failed to run command: %v", err)
	}
	return b.String(), nil
}

// newSsh creates a new SSH connection (internal function, use SshManager.CreateConnection instead)
func newSsh(ctx context.Context, host, username, password, passphrase string) (*sshConnection, error) {
	// To authenticate with the remote server you must pass at least one
	// implementation of AuthMethod via the Auth field in ClientConfig,
	// and provide a HostKeyCallback.
	conn := &sshConnection{
		client:       nil,
		clientConfig: nil,
	}

	hostInfo := readSshConfig(host)
	slog.Debug("SSH host configuration",
		"original", hostInfo.Original,
		"type", hostInfo.Type,
		"hostname", hostInfo.Hostname,
		"port", hostInfo.Port,
		"user", hostInfo.User,
		"identityFile", hostInfo.IdentityFile)

	var sshSigner ssh.Signer
	var authMethod []ssh.AuthMethod
	// Try to load SSH key if passphrase is provided
	if passphrase != "" {
		slog.Debug("attempting to unlock private key with passphrase")
		var err error
		sshSigner, err = parseSshPrivateKey(hostInfo.IdentityFile, passphrase)
		if err != nil {
			slog.Warn("failed to parse SSH private key with provided passphrase", "error", err)
			return nil, err
		}
		slog.Debug("successfully parsed SSH private key", "file", hostInfo.IdentityFile, "keyType", sshSigner.PublicKey().Type())
	}

	// Build authentication methods
	if sshSigner != nil && password != "" {
		slog.Debug("using both SSH key and password authentication")
		// Both key and password available
		authMethod = []ssh.AuthMethod{
			ssh.PublicKeys(sshSigner),
			ssh.Password(password),
		}
	} else if sshSigner != nil {
		slog.Debug("using SSH key authentication")
		// Only key available
		authMethod = []ssh.AuthMethod{
			ssh.PublicKeys(sshSigner),
		}
	} else if password != "" {
		slog.Debug("using password authentication")
		// Only password available
		authMethod = []ssh.AuthMethod{
			ssh.Password(password),
		}
	} else {
		slog.Debug("no authentication method provided (need password or SSH key with passphrase)")
		return nil, fmt.Errorf("no authentication method provided (need password or SSH key with passphrase)")
	}

	// Determine which username to use: ssh_config takes precedence over command-line
	finalUsername := username
	if hostInfo.User != "" {
		slog.Debug("using username from ssh_config", "user", hostInfo.User)
		finalUsername = hostInfo.User
	} else {
		slog.Debug("using username from command line", "user", username)
	}

	slog.Debug("SSH client configuration ready", "user", finalUsername)
	// Build ssh client config
	config := &ssh.ClientConfig{
		User: finalUsername,
		Auth: authMethod,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			// Check if user wants to skip host key verification (INSECURE)
			cfg, err := GetConfig(ctx)
			if err == nil && cfg.SkipHostKeyCheck {
				slog.Warn("⚠️  HOST KEY VERIFICATION DISABLED - INSECURE!")
				return nil
			}

			// Check if host key exists for this host
			if HostKeyExists(host) {
				// Host key exists - always verify
				if err := VerifyHostKey(host, key); err != nil {
					fp := GetHostKeyFingerprint(key)
					slog.Error("host key verification failed",
						"host", host,
						"fingerprint", fp,
						"error", err)
					return fmt.Errorf("host key verification failed: %w", err)
				}
				slog.Debug("host key verified successfully", "host", host)
				return nil
			}

			// No host key exists - check if we're in enrollment mode
			if IsEnrollmentMode(ctx) {
				// Enrollment mode - capture the host key
				fp := GetHostKeyFingerprint(key)
				slog.Info("capturing host key for first time",
					"host", host,
					"algorithm", key.Type(),
					"fingerprint", fp)
				if err := CaptureHostKey(host, key); err != nil {
					return fmt.Errorf("failed to capture host key: %w", err)
				}
				return nil
			}

			// Not in enrollment mode and no host key - fail securely
			slog.Error("no host key found", "host", host)
			return fmt.Errorf("no host key found for %s - run 'enroll' first or use '--update-hostkey'", host)
		},
		Timeout: 10 * time.Second,
	}
	conn.clientConfig = config

	// Establish the SSH connection
	address := net.JoinHostPort(hostInfo.Hostname, hostInfo.Port)
	slog.Debug("establishing SSH connection", "address", address)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		slog.Error("failed to dial", "address", address, "error", err)
		return nil, fmt.Errorf("failed to dial %s: %v", address, err.Error())
	}
	conn.client = client

	slog.Debug("SSH connection established")
	return conn, nil
}

func readSshConfig(host string) *HostInfo {
	// Step 1: Parse user input into HostInfo (the reference)
	hostInfo := ParseHost(host)

	// Step 2: Try to read from user's ssh_config for ALL host types (including IPs)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Could not determine home directory - use defaults
		return hostInfo
	}
	sshConfigFile, err := os.Open(filepath.Join(homeDir, ".ssh", "config"))
	if err != nil {
		slog.Debug("SSH config file doesn't exist or can't be read - using defaults")
		return hostInfo
	}
	defer func() { _ = sshConfigFile.Close() }()

	sshConfig, err := ssh_config.Decode(sshConfigFile)
	if err != nil || sshConfig == nil {
		slog.Debug("Failed to decode SSH config - using defaults")
		return hostInfo
	}

	// Step 3: Merge ssh_config values into HostInfo (enrich, not override)
	if hostname, _ := sshConfig.Get(host, "Hostname"); hostname != "" {
		hostInfo.Hostname = strings.ReplaceAll(hostname, "%h", host)
	}

	if user, _ := sshConfig.Get(host, "User"); user != "" {
		hostInfo.User = user
	}

	if port, _ := sshConfig.Get(host, "Port"); port != "" && port != "0" {
		hostInfo.Port = port
	}

	hostInfo.IdentityFile, _ = sshConfig.Get(host, "IdentityFile")
	hostInfo.IdentitiesOnly, _ = sshConfig.Get(host, "IdentitiesOnly")
	hostInfo.ForwardAgent, _ = sshConfig.Get(host, "ForwardAgent")
	hostInfo.HostkeyAlgorithms, _ = sshConfig.Get(host, "HostkeyAlgorithms")
	hostInfo.PubkeyAcceptedAlgorithms, _ = sshConfig.Get(host, "PubkeyAcceptedAlgorithms")

	slog.Debug("ssh_config found",
		"host", host,
		"hostname", hostInfo.Hostname,
		"port", hostInfo.Port,
		"user", hostInfo.User,
		"identityfile", hostInfo.IdentityFile)
	return hostInfo
}

func parseSshPrivateKey(identityFile, passphrase string) (ssh.Signer, error) {
	// Get current user's detail
	user, err := user.Current()
	if err != nil {
		slog.Warn("unable to get current user", "error", err)
		return nil, err
	}
	userHomeDir := user.HomeDir
	// Expand ~/ IdentityFile with full user's home path
	if strings.HasPrefix(identityFile, "~/") {
		identityFile = filepath.Join(userHomeDir, identityFile[2:])
	}

	// If Identity File found, parse private key and add ssh.PublicKeys(signer) to AuthMethod
	// Parse private key and build ssh.signer
	slog.Debug("reading SSH private key", "file", identityFile)
	key, err := os.ReadFile(identityFile)
	if err != nil {
		slog.Warn("unable to read private key", "file", identityFile, "error", err)
		return nil, err
	}
	slog.Debug("SSH private key read successfully", "file", identityFile)

	var signer ssh.Signer
	slog.Debug("unlocking private key with provided passphrase")
	signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	if err != nil {
		slog.Warn("unable to parse private key", "error", err)
		return nil, err
	}
	slog.Debug("private key parsed successfully", "keyType", signer.PublicKey().Type())
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

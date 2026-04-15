package ssh

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
)

// CreateConnection creates a new SSH connection using the provided credentials and host key manager.
// This is the main entry point for creating SSH connections in the application.
func CreateConnection(ctx context.Context, host string) (RunnerInterface, error) {
	// Get manager from context
	manager, ok := ctx.Value(core.SshManagerKey).(*SshManager)
	if !ok || manager == nil {
		return nil, fmt.Errorf("failed to get manager from context")
	}
	// Get host key manager
	hostKeyManager := NewDefaultHostKeyManager()

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before connection: %w", err)
	}

	slog.Debug("creating SSH connection", "host", host, "user", manager.getUser())

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
	authMethods, err := buildAuthMethods(hostInfo, manager.getPassword(), manager.getPassphrase())
	if err != nil {
		return nil, fmt.Errorf("failed to build auth methods: %w", err)
	}

	// Step 3: Determine which username to use
	finalUsername := manager.getUser()
	if hostInfo.User != "" {
		slog.Debug("using username from ssh_config", "user", hostInfo.User)
		finalUsername = hostInfo.User
	} else {
		slog.Debug("using username from command line", "user", manager.getUser())
	}

	// Step 4: Build SSH client config
	hostKeyCallback := BuildHostKeyCallback(ctx, host, hostKeyManager)
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
		slog.Debug("failed to dial", "address", address, "error", err)
		return nil, fmt.Errorf("%w: failed to dial %s: %w", ErrConnectionFailed, address, err)
	}

	// Perform SSH handshake
	c, chans, reqs, err := ssh.NewClientConn(netConn, address, config)
	if err != nil {
		_ = netConn.Close() // Ignore close error when handshake failed
		slog.Debug("SSH handshake failed", "address", address, "error", err)
		return nil, fmt.Errorf("%w: SSH handshake failed for %s: %w", ErrConnectionFailed, address, err)
	}

	client := ssh.NewClient(c, chans, reqs)

	slog.Debug("SSH connection established")
	return &Runner{client: client}, nil
}

// buildAuthMethods builds SSH authentication methods based on available credentials
func buildAuthMethods(hostInfo *HostInfo, password, passphrase string) ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod
	var sshSigner ssh.Signer

	// Try to load SSH key if passphrase is provided
	if passphrase != "" && hostInfo.IdentityFile != "" {
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
		authMethods = []ssh.AuthMethod{
			ssh.PublicKeys(sshSigner),
			ssh.Password(password),
		}
	} else if sshSigner != nil {
		slog.Debug("using SSH key authentication")
		authMethods = []ssh.AuthMethod{
			ssh.PublicKeys(sshSigner),
		}
	} else if password != "" {
		slog.Debug("using password authentication")
		authMethods = []ssh.AuthMethod{
			ssh.Password(password),
		}
	} else {
		slog.Debug("no authentication method provided (need password or SSH key with passphrase)")
		return nil, fmt.Errorf("no authentication method provided (need password or SSH key with passphrase)")
	}

	return authMethods, nil
}

// parseSshPrivateKey parses an SSH private key from a file
func parseSshPrivateKey(identityFile, passphrase string) (ssh.Signer, error) {
	// Get current user's detail
	currentUser, err := user.Current()
	if err != nil {
		slog.Warn("unable to get current user", "error", err)
		return nil, err
	}
	userHomeDir := currentUser.HomeDir

	// Expand ~/ IdentityFile with full user's home path
	if strings.HasPrefix(identityFile, "~/") {
		identityFile = filepath.Join(userHomeDir, identityFile[2:])
	}

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

// ParseHost analyzes a host string and returns initial HostInfo.
// This is the reference that will be enriched by ssh_config.
// It parses the host input to determine if it's an IP address, FQDN, or hostname,
// and extracts port information if provided.
func ParseHost(host string) *HostInfo {
	info := &HostInfo{
		Original: host,
		Port:     "22", // Default port
	}

	// Check if port is specified in the input
	hostPart := host
	if strings.Contains(host, ":") {
		h, p, err := net.SplitHostPort(host)
		if err == nil {
			hostPart = h
			info.Port = p
		}
	}

	// Determine host type and set initial values
	if isIPAddress(hostPart) {
		info.Type = "ip"
		info.Hostname = hostPart
		info.ShortName = hostPart
	} else if strings.Contains(hostPart, ".") {
		info.Type = "fqdn"
		info.Hostname = hostPart
		if idx := strings.Index(hostPart, "."); idx > 0 {
			info.ShortName = hostPart[:idx]
		} else {
			info.ShortName = hostPart
		}
	} else {
		info.Type = "hostname"
		info.Hostname = hostPart
		info.ShortName = hostPart
	}

	return info
}

// isIPAddress checks if string is valid IPv4/IPv6
func isIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

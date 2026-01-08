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

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
)

// CreateConnection creates a new SSH connection using the provided credentials and host key manager.
// This is the main entry point for creating SSH connections in the application.
func CreateConnection(ctx context.Context, host string, credentials CredentialsProvider, hostKeyManager HostKeyManager) (Runner, error) {
	slog.Debug("creating SSH connection", "host", host, "user", credentials.GetUser())

	// Create the connection builder
	builder := NewConnectionBuilder(&DefaultConfigReader{})

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

// readConfig reads SSH configuration for a host
func readConfig(host string) (*HostInfo, error) {
	// Step 1: Parse user input into HostInfo
	hostInfo := ParseHost(host)

	// Step 2: Try to read from user's ssh_config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return hostInfo, nil
	}

	sshConfigFile, err := os.Open(filepath.Join(homeDir, ".ssh", "config"))
	if err != nil {
		slog.Debug("SSH config file doesn't exist or can't be read - using defaults")
		return hostInfo, nil
	}
	defer func() { _ = sshConfigFile.Close() }()

	sshConfig, err := ssh_config.Decode(sshConfigFile)
	if err != nil || sshConfig == nil {
		slog.Debug("Failed to decode SSH config - using defaults")
		return hostInfo, nil
	}

	// Step 3: Merge ssh_config values into HostInfo
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

	return hostInfo, nil
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

package ssh

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"
)

// DefaultConfigReader implements ConfigReader
type DefaultConfigReader struct{}

// ReadConfig reads SSH configuration for a host
func (r *DefaultConfigReader) ReadConfig(host string) (*HostInfo, error) {
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
	if IsIPAddress(hostPart) {
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

// IsIPAddress checks if string is valid IPv4/IPv6
func IsIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

package ssh

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"
)

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

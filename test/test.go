package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
)

func main() {
	// Should come from command line
	host := "router90"
	password := "?1234567890*"

	// Read from user's ssh_config
	sshConfigFile, _ := os.Open(filepath.Join(os.Getenv("HOME"), ".ssh", "config"))
	sshConfig, _ := ssh_config.Decode(sshConfigFile)

	// Override host's config values with username provided on command line
	hostConfig := make(map[string]string)
	hostname, _ := sshConfig.Get(host, "Hostname")
	hostConfig["Hostname"] = strings.ReplaceAll(hostname, "%h", host)
	hostConfig["User"], _ = sshConfig.Get(host, "User")
	hostConfig["IdentitiesOnly"], _ = sshConfig.Get(host, "IdentitiesOnly")
	hostConfig["ForwardAgent"], _ = sshConfig.Get(host, "ForwardAgent")
	hostConfig["HostkeyAlgorithms"], _ = sshConfig.Get(host, "HostkeyAlgorithms")
	hostConfig["PubkeyAcceptedAlgorithms"], _ = sshConfig.Get(host, "PubkeyAcceptedAlgorithms")

	hostConfig["Port"], _ = sshConfig.Get(host, "Port")
	if hostConfig["Port"] == "" || hostConfig["Port"] == "0" {
		hostConfig["Port"] = "22"
	}
	hostConfig["IdentityFile"], _ = sshConfig.Get(host, "IdentityFile")

	// Get current user's detail
	user, _ := user.Current()
	userHomeDir := user.HomeDir
	// Expand ~/ IdentityFile with full user's home path
	if strings.HasPrefix(hostConfig["IdentityFile"], "~/") {
		hostConfig["IdentityFile"] = filepath.Join(userHomeDir, hostConfig["IdentityFile"][2:])
	}

	// If Identity File found, parse private key and add ssh.PublicKeys(signer) to AuthMethod
	// Parse private key and build ssh.signer
	key, err := os.ReadFile(hostConfig["IdentityFile"])
	if err != nil {
		slog.Error("unable to read private key: %v", err)
	}
	var signer ssh.Signer
	signer, err = ssh.ParsePrivateKey(key)
	if err != nil {
		slog.Error("error parsing private key: %v", err)
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(password))
		if err != nil {
			slog.Error("unable to parse private key: %v", err)
		}
	}

	// If IdentitiesOnly values "yes", we should skip the ssh.Password(password) step
	// Otherwise add it with password from command line
	authMethods := []ssh.AuthMethod{
		ssh.PublicKeys(signer),
		ssh.Password(password),
	}

	// Set up the SSH client configuration
	clientConfig := &ssh.ClientConfig{
		User:            hostConfig["User"],
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //ssh.FixedHostKey(hostKey),
		Timeout:         10 * time.Second,
	}

	// Connect to the SSH server
	connection, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", hostConfig["Hostname"], hostConfig["Port"]), clientConfig)
	if err != nil {
		slog.Error("failed to dial, %v", err)
	}
	defer connection.Close()

	// Now you can use 'connection' to interact with the remote server
	// For example, open a session:
	session, err := connection.NewSession()
	if err != nil {
		slog.Error("Failed to create session: %v", err)
	}
	defer session.Close()

	// Run a command on the remote server
	output, err := session.CombinedOutput("/system/routerboard/print")
	if err != nil {
		slog.Error("Failed to run command: %v", err)
	}
	fmt.Println(string(output))
}

package ssh

import (
	"golang.org/x/crypto/ssh"
)

// HostInfo contains SSH connection details
type HostInfo struct {
	// User input
	Original string
	Type     string // "ip", "fqdn", "hostname"

	// Connection details
	Hostname string
	Port     string
	User     string

	// For filename generation
	ShortName string

	// SSH config details
	IdentityFile             string
	IdentitiesOnly           string
	ForwardAgent             string
	HostkeyAlgorithms        string
	PubkeyAcceptedAlgorithms string
}

// HostKeyCallback provides host key validation
type HostKeyCallback func(hostname string, remote interface{}, key ssh.PublicKey) error

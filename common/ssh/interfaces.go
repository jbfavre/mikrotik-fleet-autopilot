package ssh

import (
	"golang.org/x/crypto/ssh"
)

// Runner defines the interface for SSH operations
type Runner interface {
	Close() error
	IsAlreadyClosedError(err error) bool
	Run(cmd string) (string, error)
}

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

// CredentialsProvider provides SSH credentials for connection establishment
type CredentialsProvider interface {
	GetUser() string
	GetPassword() string
	GetPassphrase() string
}

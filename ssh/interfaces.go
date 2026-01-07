package ssh

import (
	"context"

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

// ConfigReader reads SSH configuration
type ConfigReader interface {
	ReadConfig(host string) (*HostInfo, error)
}

// AuthProvider provides SSH authentication methods
type AuthProvider interface {
	BuildAuthMethods(hostInfo *HostInfo, password, passphrase string) ([]ssh.AuthMethod, error)
}

// HostKeyCallback provides host key validation
type HostKeyCallback func(hostname string, remote interface{}, key ssh.PublicKey) error

// ConnectionBuilder builds SSH connections
type ConnectionBuilder interface {
	Build(ctx context.Context, host, username, password, passphrase string, hostKeyCallback HostKeyCallback) (Runner, error)
}

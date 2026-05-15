package ssh

import (
	"fmt"
)

// SshManager encapsulates SSH credentials and provides them
// to the ssh package without exposing credentials to callers
type SshManager struct {
	user       string
	password   *string
	passphrase *string
}

// NewSshManager creates a new SSH manager with the provided credentials
// Credentials are stored privately and never exposed outside this package
func NewSshManager(user string, password, passphrase *string) *SshManager {
	return &SshManager{
		user:       user,
		password:   password,
		passphrase: passphrase,
	}
}

// GetUser returns the username
func (m *SshManager) getUser() string {
	return m.user
}

// GetPassword returns the password
func (m *SshManager) getPassword() *string {
	return m.password
}

// GetPassphrase returns the passphrase
func (m *SshManager) getPassphrase() *string {
	return m.passphrase
}

// CloneWithPassword creates a copy of the manager with a different password.
func (m *SshManager) CloneWithPassword(newPassword *string) *SshManager {
	return &SshManager{
		user:       m.user,
		password:   newPassword,
		passphrase: m.passphrase,
	}
}

// String implements fmt.Stringer interface to prevent accidental credential leaks in logs
// This ensures that if the CredentialsProvider is logged, credentials are redacted
func (m *SshManager) String() string {
	describePtr := func(p *string) string {
		switch {
		case p == nil:
			return "not set"
		case *p == "":
			return "empty (hidden)"
		default:
			return "yes (hidden)"
		}
	}

	return fmt.Sprintf("SshManager{user:%s, password:%s, passphrase:%s}",
		m.user, describePtr(m.password), describePtr(m.passphrase))
}

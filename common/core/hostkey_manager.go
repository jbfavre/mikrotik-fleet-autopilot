package core

import (
	"golang.org/x/crypto/ssh"
	sshpkg "jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// HostKeyManager encapsulates host key operations and provides them
// to the ssh package without exposing internal implementation details
type HostKeyManager struct{}

// NewHostKeyManager creates a new HostKeyManager
func NewHostKeyManager() *HostKeyManager {
	return &HostKeyManager{}
}

func (h *HostKeyManager) Exists(host string) bool {
	return HostKeyExists(host)
}

func (h *HostKeyManager) Verify(host string, key ssh.PublicKey) error {
	return VerifyHostKey(host, key)
}

func (h *HostKeyManager) Capture(host string, key ssh.PublicKey) error {
	return CaptureHostKey(host, key)
}

func (h *HostKeyManager) GetFingerprint(key ssh.PublicKey) string {
	return GetHostKeyFingerprint(key)
}

// GetHostKeyManager returns a HostKeyManager for use by the ssh package
func GetHostKeyManager() sshpkg.HostKeyManager {
	return NewHostKeyManager()
}

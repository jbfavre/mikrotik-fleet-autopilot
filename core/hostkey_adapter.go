package core

import (
	"golang.org/x/crypto/ssh"
	sshpkg "jb.favre/mikrotik-fleet-autopilot/ssh"
)

// hostKeyManagerAdapter adapts core's host key functions to ssh.HostKeyManager interface
type hostKeyManagerAdapter struct{}

func (h *hostKeyManagerAdapter) Exists(host string) bool {
	return HostKeyExists(host)
}

func (h *hostKeyManagerAdapter) Verify(host string, key ssh.PublicKey) error {
	return VerifyHostKey(host, key)
}

func (h *hostKeyManagerAdapter) Capture(host string, key ssh.PublicKey) error {
	return CaptureHostKey(host, key)
}

func (h *hostKeyManagerAdapter) GetFingerprint(key ssh.PublicKey) string {
	return GetHostKeyFingerprint(key)
}

// GetHostKeyManager returns a HostKeyManager implementation for the ssh package
func GetHostKeyManager() sshpkg.HostKeyManager {
	return &hostKeyManagerAdapter{}
}

package ssh

import (
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DefaultAuthProvider implements AuthProvider
type DefaultAuthProvider struct{}

// BuildAuthMethods builds SSH authentication methods based on available credentials
func (a *DefaultAuthProvider) BuildAuthMethods(hostInfo *HostInfo, password, passphrase string) ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod
	var sshSigner ssh.Signer

	// Try to load SSH key if passphrase is provided
	if passphrase != "" && hostInfo.IdentityFile != "" {
		slog.Debug("attempting to unlock private key with passphrase")
		var err error
		sshSigner, err = a.parseSshPrivateKey(hostInfo.IdentityFile, passphrase)
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
func (a *DefaultAuthProvider) parseSshPrivateKey(identityFile, passphrase string) (ssh.Signer, error) {
	// Get current user's detail
	user, err := user.Current()
	if err != nil {
		slog.Warn("unable to get current user", "error", err)
		return nil, err
	}
	userHomeDir := user.HomeDir

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

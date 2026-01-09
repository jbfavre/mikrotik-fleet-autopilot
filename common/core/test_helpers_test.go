package core

import (
	"crypto/ed25519"
	"crypto/rand"

	"golang.org/x/crypto/ssh"
)

// generateTestKeyEd25519 generates an ed25519 SSH key pair for testing.
// This is the preferred key type for tests as it's faster and more modern than RSA.
func generateTestKeyEd25519() (ssh.PublicKey, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, err
	}

	return signer.PublicKey(), nil
}

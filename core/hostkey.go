package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyInfo stores information about a captured SSH host key
type HostKeyInfo struct {
	Host        string    `json:"host"`
	CapturedAt  time.Time `json:"capturedAt"`
	Algorithm   string    `json:"algorithm"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"publicKey"`
}

// HostKeyFilePath returns the path to the host key file for a given host
func HostKeyFilePath(host string) string {
	hostInfo := ParseHost(host)
	return fmt.Sprintf("%s.hostkey", hostInfo.ShortName)
}

// HostKeyExists checks if a host key file exists for the given host
func HostKeyExists(host string) bool {
	path := HostKeyFilePath(host)
	_, err := os.Stat(path)
	return err == nil
}

// GetHostKeyFingerprint returns a human-readable SHA256 fingerprint of the public key
func GetHostKeyFingerprint(key ssh.PublicKey) string {
	hash := sha256.Sum256(key.Marshal())
	b64 := base64.StdEncoding.EncodeToString(hash[:])
	return fmt.Sprintf("SHA256:%s", b64)
}

// CaptureHostKey saves a host key to disk
func CaptureHostKey(host string, key ssh.PublicKey) error {
	path := HostKeyFilePath(host)

	// Prepare host key info
	info := HostKeyInfo{
		Host:        host,
		CapturedAt:  time.Now().UTC(),
		Algorithm:   key.Type(),
		Fingerprint: GetHostKeyFingerprint(key),
		PublicKey:   base64.StdEncoding.EncodeToString(key.Marshal()),
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal host key info: %w", err)
	}

	// Write to file
	slog.Debug("saving host key", "path", path, "algorithm", info.Algorithm, "fingerprint", info.Fingerprint)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write host key file: %w", err)
	}

	slog.Info("host key saved", "host", host, "file", path)
	return nil
}

// LoadHostKey loads a host key from disk
func LoadHostKey(host string) (ssh.PublicKey, error) {
	path := HostKeyFilePath(host)

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read host key file: %w", err)
	}

	// Unmarshal JSON
	var info HostKeyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal host key info: %w", err)
	}

	// Decode base64 public key
	keyBytes, err := base64.StdEncoding.DecodeString(info.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %w", err)
	}

	// Parse public key
	key, err := ssh.ParsePublicKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	slog.Debug("host key loaded", "host", host, "algorithm", info.Algorithm, "fingerprint", info.Fingerprint)
	return key, nil
}

// VerifyHostKey compares a remote host key with the stored host key
func VerifyHostKey(host string, remoteKey ssh.PublicKey) error {
	// Load stored key
	storedKey, err := LoadHostKey(host)
	if err != nil {
		return fmt.Errorf("failed to load stored host key: %w", err)
	}

	// Compare marshaled keys with bytes.Equal for correctness and simplicity
	storedBytes := storedKey.Marshal()
	remoteBytes := remoteKey.Marshal()
	if !bytes.Equal(storedBytes, remoteBytes) {
		storedFp := GetHostKeyFingerprint(storedKey)
		remoteFp := GetHostKeyFingerprint(remoteKey)
		return fmt.Errorf("host key mismatch: stored=%s remote=%s", storedFp, remoteFp)
	}

	return nil
}

// DeleteHostKey removes a host key file
func DeleteHostKey(host string) error {
	path := HostKeyFilePath(host)

	if !HostKeyExists(host) {
		return fmt.Errorf("host key file does not exist: %s", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete host key file: %w", err)
	}

	slog.Info("host key deleted", "host", host, "file", path)
	return nil
}

// LoadHostKeyInfo returns the full HostKeyInfo from disk
func LoadHostKeyInfo(host string) (*HostKeyInfo, error) {
	path := HostKeyFilePath(host)

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read host key file: %w", err)
	}

	// Unmarshal JSON
	var info HostKeyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal host key info: %w", err)
	}

	return &info, nil
}

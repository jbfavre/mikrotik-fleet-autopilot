package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyManager provides host key validation operations
type HostKeyManager interface {
	Exists(host string) bool
	Verify(host string, key ssh.PublicKey) error
	Capture(host string, key ssh.PublicKey) error
	GetFingerprint(key ssh.PublicKey) string
}

// HostKeyInfo stores information about a captured SSH host key
type HostKeyInfo struct {
	Host        string    `json:"host"`
	CapturedAt  time.Time `json:"capturedAt"`
	Algorithm   string    `json:"algorithm"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"publicKey"`
}

// DefaultHostKeyManager is a concrete implementation of HostKeyManager
type DefaultHostKeyManager struct{}

// NewDefaultHostKeyManager creates a new DefaultHostKeyManager
func NewDefaultHostKeyManager() *DefaultHostKeyManager {
	return &DefaultHostKeyManager{}
}

func (h *DefaultHostKeyManager) Exists(host string) bool {
	return HostKeyExists(host)
}

func (h *DefaultHostKeyManager) Verify(host string, key ssh.PublicKey) error {
	return VerifyHostKey(host, key)
}

func (h *DefaultHostKeyManager) Capture(host string, key ssh.PublicKey) error {
	return CaptureHostKey(host, key)
}

func (h *DefaultHostKeyManager) GetFingerprint(key ssh.PublicKey) string {
	return GetHostKeyFingerprint(key)
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

// BuildHostKeyCallback creates a host key callback function that integrates with the host key manager
func BuildHostKeyCallback(ctx context.Context, host string, manager HostKeyManager) HostKeyCallback {
	return func(hostname string, remote interface{}, key ssh.PublicKey) error {
		// Extract config from context to check SkipHostKeyCheck
		type configGetter interface {
			GetSkipHostKeyCheck() bool
		}

		// Debug: check what's in the context
		slog.Debug("host key callback invoked",
			"host", host,
			"configKey", ctx.Value("config") != nil,
			"enrollmentKey", ctx.Value("enrollment") != nil)

		// Check if user wants to skip host key verification (INSECURE)
		var skipVerification bool
		if ctxValue := ctx.Value("config"); ctxValue != nil {
			if cfg, ok := ctxValue.(configGetter); ok {
				skipVerification = cfg.GetSkipHostKeyCheck()
			}
		}

		if skipVerification {
			slog.Warn("⚠️  HOST KEY VERIFICATION DISABLED - INSECURE!")

			// Even when skipping verification, still capture the host key during enrollment
			isEnrollment := false
			if ctxValue := ctx.Value("enrollment"); ctxValue != nil {
				if val, ok := ctxValue.(bool); ok {
					isEnrollment = val
				}
			}

			if !manager.Exists(host) && isEnrollment {
				fp := manager.GetFingerprint(key)
				slog.Info("capturing host key for first time (verification skipped)",
					"host", host,
					"algorithm", key.Type(),
					"fingerprint", fp)
				if err := manager.Capture(host, key); err != nil {
					slog.Error("failed to capture host key while SkipHostKeyCheck is enabled",
						"host", host,
						"algorithm", key.Type(),
						"fingerprint", fp,
						"error", err)
				}
			}
			return nil
		}

		// Check if host key exists for this host
		if manager.Exists(host) {
			// Host key exists - always verify
			if err := manager.Verify(host, key); err != nil {
				fp := manager.GetFingerprint(key)
				slog.Error("host key verification failed",
					"host", host,
					"fingerprint", fp,
					"error", err)
				return fmt.Errorf("host key verification failed: %w", err)
			}
			slog.Debug("host key verified successfully", "host", host)
			return nil
		}

		// No host key exists - check if we're in enrollment mode
		isEnrollment := false
		if ctxValue := ctx.Value("enrollment"); ctxValue != nil {
			if val, ok := ctxValue.(bool); ok {
				isEnrollment = val
			}
		}

		if isEnrollment {
			// Enrollment mode - capture the host key
			fp := manager.GetFingerprint(key)
			slog.Info("capturing host key for first time",
				"host", host,
				"algorithm", key.Type(),
				"fingerprint", fp)
			if err := manager.Capture(host, key); err != nil {
				return fmt.Errorf("failed to capture host key: %w", err)
			}
			return nil
		}

		// Not in enrollment mode and no host key - fail securely
		slog.Error("no host key found", "host", host)
		return fmt.Errorf("no host key found for %s - run 'enroll' command first to capture the host key", host)
	}
}

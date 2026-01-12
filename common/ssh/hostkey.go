package ssh

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/ssh"
)

// ContextKey is a custom type for context keys to avoid collisions
type ContextKey string

const (
	// ConfigKey is the context key for storing Config (must match core.ConfigKey)
	ConfigKey ContextKey = "config"
	// EnrollmentKey is the context key for storing enrollment mode (must match core.EnrollmentKey)
	EnrollmentKey ContextKey = "enrollment"
)

// HostKeyManager provides host key validation operations
type HostKeyManager interface {
	Exists(host string) bool
	Verify(host string, key ssh.PublicKey) error
	Capture(host string, key ssh.PublicKey) error
	GetFingerprint(key ssh.PublicKey) string
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

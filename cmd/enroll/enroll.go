package enroll

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var hostname string
var preEnrollScript string
var postEnrollScript string
var skipUpdates bool
var skipExport bool
var outputDir string
var force bool
var updateHostKeyOnly bool

// sshConnectionFactory is the factory function for creating SSH connections
// This can be overridden in tests to inject mock SSH manager
var sshConnectionFactory = core.CreateConnection

// applyUpdatesFunc is the function for applying updates
// This can be overridden in tests to inject mock behavior
var applyUpdatesFunc = updates.ApplyUpdates

// exportConfigFunc is the function for exporting configuration
// This can be overridden in tests to inject mock behavior
var exportConfigFunc = export.ExportConfig

var Command = []*cli.Command{
	{
		Name:  "enroll",
		Usage: "Enroll a bare MikroTik router with initial configuration",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "hostname",
				Value:       "",
				Usage:       "Router hostname/identity to set (e.g., router1). Required for enrollment, not needed when using --update-hostkey-only.",
				Destination: &hostname,
			},
			&cli.StringFlag{
				Name:        "pre-enroll-script",
				Value:       "./pre-enroll.rsc",
				Usage:       "Path to RouterOS commands file to apply",
				Destination: &preEnrollScript,
			},
			&cli.StringFlag{
				Name:        "post-enroll-script",
				Value:       "./post-enroll.rsc",
				Usage:       "Path to RouterOS commands file to apply",
				Destination: &postEnrollScript,
			},
			&cli.BoolFlag{
				Name:        "skip-updates",
				Value:       false,
				Usage:       "Skip checking/applying updates during enrollment",
				Destination: &skipUpdates,
			},
			&cli.BoolFlag{
				Name:        "skip-export",
				Value:       false,
				Usage:       "Skip exporting configuration after enrollment",
				Destination: &skipExport,
			},
			&cli.StringFlag{
				Name:        "output-dir",
				Value:       ".",
				Usage:       "Directory where to save the exported configuration",
				Destination: &outputDir,
			},
			&cli.BoolFlag{
				Name:        "force",
				Aliases:     []string{"f"},
				Value:       false,
				Usage:       "Force re-enrollment of an already enrolled device (removes existing config and host key, performs full enrollment)",
				Destination: &force,
			},
			&cli.BoolFlag{
				Name:        "update-hostkey-only",
				Value:       false,
				Usage:       "Only update the SSH host key without performing full enrollment. Supports batch mode when multiple hosts are discovered. (useful after SSH key rotation, reinstall, or SSH upgrade)",
				Destination: &updateHostKeyOnly,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := core.GetConfig(ctx)
			if err != nil {
				slog.Debug("failed to get global config", "error", err)
				return err
			}

			// Validate flag combination
			if force && updateHostKeyOnly {
				return fmt.Errorf("cannot use --force and --update-hostkey-only together")
			}

			// Set enrollment mode in context to allow host key capture
			ctx = context.WithValue(ctx, core.EnrollmentModeKey, true)
			slog.Debug("enrollment mode enabled in context")

			// Handle update-hostkey-only mode (supports batch processing)
			if updateHostKeyOnly {
				// Batch mode: update hostkeys for all discovered hosts
				if len(cfg.Hosts) > 1 {
					slog.Info("batch updating SSH host keys", "count", len(cfg.Hosts))

					successCount := 0
					failCount := 0
					var lastErr error

					for _, host := range cfg.Hosts {
						fingerprint, err := updateHostKey(ctx, host)
						if err != nil {
							slog.Error("host key update failed", "host", host, "error", err)
							fmt.Printf("❌ %s: Host key update failed\n", host)
							failCount++
							lastErr = err
							// Continue with other hosts
						} else {
							slog.Info("host key update completed successfully", "host", host)
							fmt.Printf("✅ %s: Host key updated (%s)\n", host, fingerprint)
							successCount++
						}
					}

					if failCount > 0 && successCount == 0 {
						return fmt.Errorf("all host key updates failed")
					} else if failCount > 0 {
						return fmt.Errorf("some host key updates failed: %w", lastErr)
					}
					return nil
				}

				// Single host mode
				if len(cfg.Hosts) != 1 {
					return fmt.Errorf("no hosts specified or discovered")
				}

				host := cfg.Hosts[0]
				slog.Info("updating SSH host key only", "host", host)
				fingerprint, err := updateHostKey(ctx, host)
				if err != nil {
					slog.Error("host key update failed", "host", host, "error", err)
					fmt.Printf("❌ Host key update failed\n")
					return err
				}
				slog.Info("host key update completed successfully", "host", host)
				fmt.Printf("✅ Host key updated (%s)\n", fingerprint)
				return nil
			}

			// Normal enrollment requires exactly one host and hostname
			if len(cfg.Hosts) != 1 {
				slog.Debug("enroll command requires exactly one host", "got", len(cfg.Hosts))
				return fmt.Errorf("enroll command requires exactly one host, got %d", len(cfg.Hosts))
			}

			if hostname == "" {
				return fmt.Errorf("--hostname is required for enrollment")
			}

			host := cfg.Hosts[0]

			// Handle force re-enrollment
			if force {
				slog.Info("force re-enrollment requested", "host", host)
				if err := deleteExistingEnrollment(host); err != nil {
					slog.Error("failed to remove existing enrollment", "host", host, "error", err)
					return fmt.Errorf("failed to remove existing enrollment: %w", err)
				}
			}

			// Perform normal enrollment
			if err := enroll(ctx, host); err != nil {
				slog.Error("enrollment failed", "host", host, "error", err)
				fmt.Printf("❌ Enrollment failed\n")
			} else {
				slog.Info("enrollment completed successfully", "host", host)
				fmt.Printf("✅ Enrollment completed successfully\n")
			}

			return err
		},
	},
}

func enroll(ctx context.Context, host string) error {
	slog.Info("starting enrollment", "host", host)

	// Connect to router
	slog.Debug("connecting to router", "host", host)
	conn, err := sshConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to connect to router", "host", host, "error", err)
		fmt.Printf("❌ Failed to connect\n")
		return fmt.Errorf("failed to connect to router: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	slog.Debug("successfully connected", "host", host)

	// Step 1: Apply pre-enroll configuration file
	slog.Debug("applying pre-enroll configuration file")
	if err := applyConfigFile(conn, preEnrollScript); err != nil {
		slog.Error("failed to apply pre-enroll configuration file", "error", err)
		fmt.Printf("❌ Pre-enroll configuration failed\n")
		return fmt.Errorf("failed to apply pre-enroll configuration file: %w", err)
	}
	slog.Debug("pre-enroll configuration applied")
	fmt.Printf("✅ Pre-enroll configuration applied\n")

	// Step 2: Set router identity
	slog.Debug("setting router identity", "hostname", hostname)
	if err := setRouterIdentity(conn, hostname); err != nil {
		slog.Error("failed to set router identity", "error", err)
		fmt.Printf("❌ Identity set failed\n")
		return fmt.Errorf("failed to set router identity: %w", err)
	}
	slog.Debug("router identity set", "host", host, "hostname", hostname)
	fmt.Printf("✅ Router identity set\n")

	// Step 3: Apply updates (unless skipped)
	if !skipUpdates {
		slog.Debug("checking and applying updates", "host", host)
		if err := applyUpdatesFunc(ctx, host); err != nil {
			slog.Error("failed to apply updates", "host", host, "error", err)
			fmt.Printf("⚠️  Updates failed (non-fatal)\n")
			// Non fatal error, no return
		}
		// No need for a specific status here since it's already managed by the updates subcommand
	} else {
		slog.Debug("skipping updates")
		fmt.Printf("❓ Updates skipped\n")
	}

	// Step 4: Export configuration (unless skipped)
	// Export properly manages its own SSH connection
	if !skipExport {
		slog.Debug("exporting final configuration", "host", host)
		if err := exportConfigFunc(ctx, host, outputDir, false, hostname); err != nil {
			slog.Error("failed to export configuration", "host", host, "error", err)
			return fmt.Errorf("failed to export configuration: %w", err)
		}
		// Export closes its SSH connection, invalidating our SSH connection
		// Recreate the connection to ensure subsequent steps work properly
		slog.Debug("recreating SSH connection after export", "host", host)
		_ = conn.Close()
		conn, err = sshConnectionFactory(ctx, host)
		if err != nil {
			slog.Error("failed to reconnect after export", "host", host, "error", err)
			return fmt.Errorf("failed to reconnect after export: %w", err)
		}
		slog.Debug("reconnected after export", "host", host)
		// No need for a specific status here since it's already managed by the export subcommand
	} else {
		slog.Debug("skipping export")
		fmt.Printf("❓ Export skipped\n")
	}

	// Step 5: Apply post-enroll configuration file
	slog.Debug("applying post-enroll configuration file")
	if err := applyConfigFile(conn, postEnrollScript); err != nil {
		slog.Error("failed to apply post-enroll configuration file", "error", err)
		fmt.Printf("❌ Post-enroll configuration failed\n")
		return fmt.Errorf("failed to apply post-enroll configuration file: %w", err)
	}
	slog.Debug("post-enroll configuration file applied")
	fmt.Printf("✅ Post-enroll configuration applied\n")

	return nil
}

// applyConfigFile reads and executes RouterOS commands from a file
func applyConfigFile(conn core.SshRunner, filePath string) error {
	// Read file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open config file %s: %w", filePath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	// Parse and execute commands line by line
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		slog.Debug("executing command", "line", lineNum, "command", line)
		_, err := conn.Run(line)
		if err != nil {
			return fmt.Errorf("failed to execute command at line %d (%s): %w", lineNum, line, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	return nil
}

// setRouterIdentity sets the system identity (hostname) on the router
func setRouterIdentity(conn core.SshRunner, hostname string) error {
	cmd := fmt.Sprintf("/system identity set name=%s", hostname)
	slog.Debug("setting identity with command", "hostname", hostname, "command", cmd)
	_, err := conn.Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to set identity: %w", err)
	}
	return nil
}

// updateHostKey captures the SSH host key for the first time or updates an existing one,
// without performing full enrollment.
func updateHostKey(ctx context.Context, host string) (string, error) {
	slog.Info("starting host key update", "host", host)

	// Load existing host key info if it exists
	oldInfo, err := core.LoadHostKeyInfo(host)
	if err == nil {
		slog.Debug("loaded existing host key", "host", host, "algorithm", oldInfo.Algorithm, "fingerprint", oldInfo.Fingerprint)
	} else {
		slog.Debug("no existing host key found", "host", host)
	}

	// Create SSH connection (this will capture the new host key)
	slog.Debug("connecting to router to capture new host key", "host", host)
	conn, err := sshConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to connect to router", "host", host, "error", err)
		return "", fmt.Errorf("failed to connect to device: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	slog.Debug("successfully connected and captured host key", "host", host)

	// Load new host key info
	newInfo, err := core.LoadHostKeyInfo(host)
	if err != nil {
		slog.Error("failed to load new host key", "host", host, "error", err)
		return "", fmt.Errorf("failed to load new host key: %w", err)
	}

	// Log the details
	if oldInfo != nil {
		if oldInfo.Fingerprint == newInfo.Fingerprint {
			slog.Debug("host key unchanged", "host", host, "algorithm", newInfo.Algorithm, "fingerprint", newInfo.Fingerprint)
		} else {
			slog.Warn("host key changed", "host", host, "old_algorithm", oldInfo.Algorithm, "old_fingerprint", oldInfo.Fingerprint, "new_algorithm", newInfo.Algorithm, "new_fingerprint", newInfo.Fingerprint)
		}
	} else {
		slog.Info("host key captured for first time", "host", host, "algorithm", newInfo.Algorithm, "fingerprint", newInfo.Fingerprint)
	}

	return newInfo.Fingerprint, nil
}

// deleteExistingEnrollment removes all enrollment artifacts for a host
func deleteExistingEnrollment(host string) error {
	slog.Info("deleting existing enrollment artifacts", "host", host)

	// Delete host key
	if core.HostKeyExists(host) {
		slog.Debug("deleting host key", "host", host)
		if err := core.DeleteHostKey(host); err != nil {
			slog.Error("failed to delete host key", "host", host, "error", err)
			return fmt.Errorf("failed to delete host key: %w", err)
		}
		fmt.Printf("Removed existing host key for %s\n", host)
	}

	// Delete config file
	parsedHost := core.ParseHost(host)
	configFile := fmt.Sprintf("%s.rsc", parsedHost.ShortName)
	if _, err := os.Stat(configFile); err == nil {
		slog.Debug("deleting config file", "file", configFile)
		if err := os.Remove(configFile); err != nil {
			slog.Error("failed to delete config file", "file", configFile, "error", err)
			return fmt.Errorf("failed to delete config file: %w", err)
		}
		fmt.Printf("Removed existing config file %s\n", configFile)
	}

	slog.Info("existing enrollment artifacts deleted", "host", host)
	return nil
}

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
	core "jb.favre/mikrotik-fleet-autopilot/common/core"
	sshpkg "jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// Config holds all enrollment configuration options
type Config struct {
	Hostname          string
	PreEnrollScript   string
	PostEnrollScript  string
	SkipUpdates       bool
	SkipExport        bool
	OutputDir         string
	Force             bool
	UpdateHostKeyOnly bool
}

// Dependencies holds injectable dependencies for testing
type Dependencies struct {
	SSHConnectionFactory func(context.Context, string) (sshpkg.Runner, error)
	ApplyUpdatesFunc     func(context.Context, string) error
	ExportConfigFunc     func(context.Context, string, string, bool, string) error
}

var Command = []*cli.Command{
	{
		Name:  "enroll",
		Usage: "Enroll a bare MikroTik router with initial configuration",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "hostname",
				Value: "",
				Usage: "Router hostname/identity to set (e.g., router1). Required for enrollment, not needed when using --update-hostkey-only.",
			},
			&cli.StringFlag{
				Name:  "pre-enroll-script",
				Value: "./pre-enroll.rsc",
				Usage: "Path to RouterOS commands file to apply",
			},
			&cli.StringFlag{
				Name:  "post-enroll-script",
				Value: "./post-enroll.rsc",
				Usage: "Path to RouterOS commands file to apply",
			},
			&cli.BoolFlag{
				Name:  "skip-updates",
				Value: false,
				Usage: "Skip checking/applying updates during enrollment",
			},
			&cli.BoolFlag{
				Name:  "skip-export",
				Value: false,
				Usage: "Skip exporting configuration after enrollment",
			},
			&cli.StringFlag{
				Name:  "output-dir",
				Value: ".",
				Usage: "Directory where to save the exported configuration",
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Value:   false,
				Usage:   "Force re-enrollment of an already enrolled device (removes existing config and host key, performs full enrollment)",
			},
			&cli.BoolFlag{
				Name:  "update-hostkey-only",
				Value: false,
				Usage: "Only update the SSH host key without performing full enrollment. Supports batch mode when multiple hosts are discovered. (useful after SSH key rotation, reinstall, or SSH upgrade)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return enroll(ctx, cmd)
		},
	},
}

// enroll is the entry point for the enrollment command
func enroll(ctx context.Context, cmd *cli.Command) error {
	coreCfg, err := core.GetConfig(ctx)
	if err != nil {
		slog.Debug("failed to get global config", "error", err)
		return err
	}

	// Build enrollment configuration from CLI flags
	enrollCfg := Config{
		Hostname:          cmd.String("hostname"),
		PreEnrollScript:   cmd.String("pre-enroll-script"),
		PostEnrollScript:  cmd.String("post-enroll-script"),
		SkipUpdates:       cmd.Bool("skip-updates"),
		SkipExport:        cmd.Bool("skip-export"),
		OutputDir:         cmd.String("output-dir"),
		Force:             cmd.Bool("force"),
		UpdateHostKeyOnly: cmd.Bool("update-hostkey-only"),
	}

	// Validate flag combination
	if enrollCfg.Force && enrollCfg.UpdateHostKeyOnly {
		return fmt.Errorf("cannot use --force and --update-hostkey-only together")
	}

	// Set enrollment mode in context to allow host key capture
	ctx = context.WithValue(ctx, core.EnrollmentKey, true)
	slog.Debug("enrollment mode enabled in context")

	// Build dependencies for all operations
	deps := Dependencies{
		SSHConnectionFactory: core.CreateConnection,
		ApplyUpdatesFunc:     updates.Updates,
		ExportConfigFunc:     export.Export,
	}

	// Route to appropriate operation mode
	if enrollCfg.UpdateHostKeyOnly {
		return updateHostKeysOnly(ctx, coreCfg.Hosts, deps)
	}
	return enrollSingleHost(ctx, coreCfg.Hosts, enrollCfg, deps)
}

// updateHostKeysOnly processes host key updates for one or more hosts
func updateHostKeysOnly(ctx context.Context, hosts []string, deps Dependencies) error {
	// Batch mode: update hostkeys for all discovered hosts
	if len(hosts) > 1 {
		slog.Info("batch updating SSH host keys", "count", len(hosts))

		successCount := 0
		failCount := 0
		var lastErr error

		for _, host := range hosts {
			fingerprint, err := updateHostKey(ctx, host, deps)
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
	if len(hosts) != 1 {
		return fmt.Errorf("no hosts specified or discovered")
	}

	host := hosts[0]
	slog.Info("updating SSH host key only", "host", host)
	fingerprint, err := updateHostKey(ctx, host, deps)
	if err != nil {
		slog.Error("host key update failed", "host", host, "error", err)
		fmt.Printf("❌ Host key update failed\n")
		return err
	}
	slog.Info("host key update completed successfully", "host", host)
	fmt.Printf("✅ Host key updated (%s)\n", fingerprint)
	return nil
}

// enrollSingleHost processes full enrollment for a single host
func enrollSingleHost(ctx context.Context, hosts []string, cfg Config, deps Dependencies) error {
	// Validate that we have exactly one host
	if len(hosts) != 1 {
		slog.Debug("enroll command requires exactly one host", "got", len(hosts))
		return fmt.Errorf("enroll command requires exactly one host, got %d", len(hosts))
	}

	// Validate that hostname is provided
	if cfg.Hostname == "" {
		return fmt.Errorf("--hostname is required for enrollment")
	}

	host := hosts[0]

	// Handle force re-enrollment by removing existing artifacts
	if cfg.Force {
		slog.Info("force re-enrollment requested", "host", host)
		if err := deleteExistingEnrollment(host); err != nil {
			slog.Error("failed to remove existing enrollment", "host", host, "error", err)
			return fmt.Errorf("failed to remove existing enrollment: %w", err)
		}
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	slog.Info("starting enrollment", "host", host)

	// Step 0: Capture and save SSH host key
	fingerprint, err := updateHostKey(ctx, host, deps)
	if err != nil {
		slog.Error("failed to capture host key", "host", host, "error", err)
		fmt.Printf("❌ Host key capture failed\n")
		return fmt.Errorf("failed to capture host key: %w", err)
	}
	slog.Debug("host key captured", "host", host, "fingerprint", fingerprint)
	fmt.Printf("✅ Host key captured (%s)\n", fingerprint)

	// Establish connection
	conn, err := connectToRouter(ctx, host, deps)
	if err != nil {
		return err
	}
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	// Step 1: Apply pre-enrollment script
	if err := applyPreEnrollScript(conn, cfg); err != nil {
		slog.Error("enrollment failed", "host", host, "error", err)
		fmt.Printf("❌ Enrollment failed\n")
		return err
	}

	// Step 2: Set router identity
	if err := setRouterIdentity(conn, cfg.Hostname); err != nil {
		slog.Error("failed to set router identity", "error", err)
		fmt.Printf("❌ Identity set failed\n")
		return fmt.Errorf("failed to set router identity: %w", err)
	}
	slog.Debug("router identity set", "host", host, "hostname", cfg.Hostname)
	fmt.Printf("✅ Router identity set\n")

	// Step 3: Apply updates (optional)
	if err := applyUpdates(ctx, host, cfg, deps); err != nil {
		// Non-fatal, continue
	}

	// Step 4: Export configuration (optional)
	conn, err = exportConfiguration(ctx, host, cfg, deps, conn)
	if err != nil {
		slog.Error("enrollment failed", "host", host, "error", err)
		fmt.Printf("❌ Enrollment failed\n")
		return err
	}

	// Step 5: Apply post-enrollment script
	if err := applyPostEnrollScript(conn, cfg); err != nil {
		slog.Error("enrollment failed", "host", host, "error", err)
		fmt.Printf("❌ Enrollment failed\n")
		return err
	}

	slog.Info("enrollment completed successfully", "host", host)
	fmt.Printf("✅ Enrollment completed successfully\n")
	return nil
}

// connectToRouter establishes an SSH connection to the router
func connectToRouter(ctx context.Context, host string, deps Dependencies) (sshpkg.Runner, error) {
	slog.Debug("connecting to router", "host", host)
	conn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to connect to router", "host", host, "error", err)
		fmt.Printf("❌ Failed to connect\n")
		return nil, fmt.Errorf("failed to connect to router: %w", err)
	}
	slog.Debug("successfully connected", "host", host)
	return conn, nil
}

// applyPreEnrollScript applies the pre-enrollment configuration script
func applyPreEnrollScript(conn sshpkg.Runner, cfg Config) error {
	slog.Debug("applying pre-enroll configuration file")
	if err := applyConfigFile(conn, cfg.PreEnrollScript); err != nil {
		slog.Error("failed to apply pre-enroll configuration file", "error", err)
		fmt.Printf("❌ Pre-enroll configuration failed\n")
		return fmt.Errorf("failed to apply pre-enroll configuration file: %w", err)
	}
	slog.Debug("pre-enroll configuration applied")
	fmt.Printf("✅ Pre-enroll configuration applied\n")
	return nil
}

// applyUpdates applies system updates unless skipped
func applyUpdates(ctx context.Context, host string, cfg Config, deps Dependencies) error {
	if cfg.SkipUpdates {
		slog.Debug("skipping updates")
		fmt.Printf("❓ Updates skipped\n")
		return nil
	}

	slog.Debug("checking and applying updates", "host", host)
	if err := deps.ApplyUpdatesFunc(ctx, host); err != nil {
		slog.Error("failed to apply updates", "host", host, "error", err)
		fmt.Printf("⚠️  Updates failed (non-fatal)\n")
		// Non-fatal error
	}
	return nil
}

// exportConfiguration exports the router configuration and recreates SSH connection
func exportConfiguration(ctx context.Context, host string, cfg Config, deps Dependencies, conn sshpkg.Runner) (sshpkg.Runner, error) {
	if cfg.SkipExport {
		slog.Debug("skipping export")
		fmt.Printf("❓ Export skipped\n")
		return conn, nil
	}

	slog.Debug("exporting final configuration", "host", host)
	if err := deps.ExportConfigFunc(ctx, host, cfg.OutputDir, false, cfg.Hostname); err != nil {
		slog.Error("failed to export configuration", "host", host, "error", err)
		return nil, fmt.Errorf("failed to export configuration: %w", err)
	}

	// Export closes its SSH connection, so we need to reconnect
	slog.Debug("recreating SSH connection after export", "host", host)
	_ = conn.Close()
	newConn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to reconnect after export", "host", host, "error", err)
		return nil, fmt.Errorf("failed to reconnect after export: %w", err)
	}
	slog.Debug("reconnected after export", "host", host)
	return newConn, nil
}

// applyPostEnrollScript applies the post-enrollment configuration script
func applyPostEnrollScript(conn sshpkg.Runner, cfg Config) error {
	slog.Debug("applying post-enroll configuration file")
	if err := applyConfigFile(conn, cfg.PostEnrollScript); err != nil {
		slog.Error("failed to apply post-enroll configuration file", "error", err)
		fmt.Printf("❌ Post-enroll configuration failed\n")
		return fmt.Errorf("failed to apply post-enroll configuration file: %w", err)
	}
	slog.Debug("post-enroll configuration file applied")
	fmt.Printf("✅ Post-enroll configuration applied\n")
	return nil
}

// applyConfigFile reads and executes RouterOS commands from a file
func applyConfigFile(conn sshpkg.Runner, filePath string) error {
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
func setRouterIdentity(conn sshpkg.Runner, hostname string) error {
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
func updateHostKey(ctx context.Context, host string, deps Dependencies) (string, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled: %w", err)
	}

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
	conn, err := deps.SSHConnectionFactory(ctx, host)
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
	parsedHost := sshpkg.ParseHost(host)
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

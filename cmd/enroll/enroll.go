package enroll

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/display"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// EnrollConfig holds all enrollment configuration options
type EnrollConfig struct {
	Hostname           string
	PreEnrollScript    string
	PostEnrollScript   string
	SkipUpdates        bool
	SkipExport         bool
	OutputDir          string
	Force              bool
	UpdateHostKeyOnly  bool
	Debug              bool
	MaxConcurrentHosts int
	PreferLiveMode     bool
}

// EnrollDependencies holds injectable dependencies for testing
type EnrollDependencies struct {
	SSHConnectionFactory func(context.Context, string) (ssh.RunnerInterface, error)
	ApplyUpdatesFunc     func(context.Context, string) error
	ExportConfigFunc     func(context.Context, string, string, bool, string) error
}

type deleteExistingEnrollmentResult struct {
	hostKeyDeleted bool
	configDeleted  string
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
			coreCfg, err := core.GetConfig(ctx)
			if err != nil {
				slog.Debug("failed to get global config", "error", err)
				return err
			}

			// Build enrollment configuration from CLI flags
			enrollCfg := EnrollConfig{
				Hostname:           cmd.String("hostname"),
				PreEnrollScript:    cmd.String("pre-enroll-script"),
				PostEnrollScript:   cmd.String("post-enroll-script"),
				SkipUpdates:        cmd.Bool("skip-updates"),
				SkipExport:         cmd.Bool("skip-export"),
				OutputDir:          cmd.String("output-dir"),
				Force:              cmd.Bool("force"),
				UpdateHostKeyOnly:  cmd.Bool("update-hostkey-only"),
				Debug:              coreCfg.Debug,
				MaxConcurrentHosts: coreCfg.EffectiveMaxConcurrent,
				PreferLiveMode:     coreCfg.PreferLiveMode,
			}

			// Build dependencies for all operations
			deps := EnrollDependencies{
				SSHConnectionFactory: ssh.CreateConnection,
				ApplyUpdatesFunc:     updates.Updates,
				ExportConfigFunc:     export.Export,
			}

			return runEnrollForHosts(ctx, coreCfg.Hosts, enrollCfg, deps, os.Stdout)
		},
	},
}

func runEnrollForHosts(ctx context.Context, hosts []string, cfg EnrollConfig, deps EnrollDependencies, out io.Writer) error {
	if cfg.Force && cfg.UpdateHostKeyOnly {
		return fmt.Errorf("cannot use --force and --update-hostkey-only together")
	}
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts specified or discovered")
	}
	if !cfg.UpdateHostKeyOnly {
		if len(hosts) != 1 {
			return fmt.Errorf("enroll command requires exactly one host, got %d", len(hosts))
		}
		if cfg.Hostname == "" {
			return fmt.Errorf("--hostname is required for enrollment")
		}
	}

	disp := display.New(out, hosts, display.InitOptions{
		Debug:          cfg.Debug,
		PreferLiveMode: cfg.PreferLiveMode,
		Concurrent:     cfg.MaxConcurrentHosts > 1,
	})
	core.SetLiveLogWriter(disp.LogWriter())

	// errs is indexed by host position so results are collected in host-list
	// order regardless of goroutine completion order.
	errs := make([]error, len(hosts))
	var wg sync.WaitGroup

	processHost := func(i int, host string) {
		line := disp.Line(i)
		displayStepCallback := display.NewStepCallback(line)
		err := enroll(ctx, host, cfg, deps, displayStepCallback)
		switch {
		case errors.Is(err, ssh.ErrConnectionFailed):
                        line.CompleteStep("❓")
                        line.Finish("❓", err.Error())
		case err != nil:
			line.CompleteStep("❌")
			line.Finish("❌", fmt.Sprintf("Enrollment failed: %v", err))
			errs[i] = err
			slog.Error("enrollment failed", "host", host, "hostname", cfg.Hostname, "error", err)
		default:
			slog.Info("enrollment completed successfully", "host", host, "hostname", cfg.Hostname)
			line.Finish("✅", "Enrollment completed successfully")
		}
	}

	sem := make(chan struct{}, cfg.MaxConcurrentHosts)
	var ctxErr error
loop:
	for i, host := range hosts {
		if cfg.MaxConcurrentHosts <= 1 {
			processHost(i, host)
		} else {
			wg.Add(1)
			select {
			case sem <- struct{}{}:
				go func(idx int, h string) {
					defer wg.Done()
					defer func() { <-sem }()
					processHost(idx, h)
				}(i, host)
			case <-ctx.Done():
				wg.Done()
				ctxErr = ctx.Err()
				break loop
			}
		}
	}
	wg.Wait()
	result := errors.Join(append(errs, ctxErr)...)
	disp.Stop()
	core.SetLiveLogWriter(nil)
	return result
}

// enroll is the entry point for the enrollment command
func enroll(ctx context.Context, host string, enrollCfg EnrollConfig, deps EnrollDependencies, displayStepCallback display.StepCallback) error {
	// Internal helper function to manage display updates if a callback is provided, or do nothing if not.
	reportStep := func(icon, msg string) {
		if displayStepCallback != nil {
			displayStepCallback(icon, msg)
		}
	}

	// Set enrollment mode in context to allow host key capture
	ctx = context.WithValue(ctx, core.EnrollmentKey, true)
	slog.Debug("enrollment mode enabled in context")

	hostname := enrollCfg.Hostname
	// Handle force re-enrollment by removing existing artifacts
	if enrollCfg.Force {
		slog.Info("force re-enrollment requested", "host", host)
		reportStep("⏳", "Removing existing enrollment artifacts…")
		cleanupResult, err := deleteExistingEnrollment(host, hostname)
		if err != nil {
			slog.Error("failed to remove existing enrollment", "host", host, "hostname", hostname, "error", err)
			return fmt.Errorf("failed to remove existing enrollment: %w", err)
		}
		switch {
		case cleanupResult.hostKeyDeleted && cleanupResult.configDeleted != "":
			reportStep("⏳", fmt.Sprintf("Removed existing host key and config file %s", cleanupResult.configDeleted))
		case cleanupResult.hostKeyDeleted:
			reportStep("⏳", "Removed existing host key")
		case cleanupResult.configDeleted != "":
			reportStep("⏳", fmt.Sprintf("Removed existing config file %s", cleanupResult.configDeleted))
		default:
			reportStep("⏳", "No existing enrollment artifacts found")
		}
		reportStep("✅", "Removed existing enrollment artifacts")
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	slog.Info("starting enrollment", "host", host, "hostname", hostname)

	// Step 1: Always update host key first
	reportStep("⏳", "Updating SSH host key…")
	fingerprint, err := updateHostKey(ctx, host, deps)
	if err != nil {
		slog.Error("failed to capture host key", "host", host, "error", err)
		return fmt.Errorf("failed to capture host key: %w", err)
	}
	reportStep("✅", "SSH host key successfully updated")
	slog.Debug("host key captured", "host", host, "hostname", hostname, "fingerprint", fingerprint)

	// Handle update-hostkey-only mode
	if enrollCfg.UpdateHostKeyOnly {
		// Once key is updated, we're done
		return nil
	}

	// Step 2: Establish connection
	reportStep("⏳", "Connecting to router…")
	conn, err := connectToRouter(ctx, host, hostname, deps)
	if err != nil {
		return err
	}
	reportStep("✅", "Connected")
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	// Step 3: Apply pre-enrollment script
	reportStep("⏳", "Applying pre-enroll script…")
	if err := applyPreEnrollScript(conn, enrollCfg); err != nil {
		slog.Error("pre-enroll script failed", "host", host, "hostname", hostname, "error", err)
		return fmt.Errorf("failed to apply pre-enroll script: %w", err)
	}
	reportStep("✅", "Pre-enroll script applied successfully")

	// Step 4: Set router identity
	reportStep("⏳", "Setting router identity…")
	if err := setRouterIdentity(conn, hostname); err != nil {
		slog.Error("failed to set router identity", "host", host, "hostname", hostname, "error", err)
		return fmt.Errorf("failed to set router identity: %w", err)
	}
	reportStep("✅", "Router identity set successfully")

	// Step 5: Apply updates (optional)
	if enrollCfg.SkipUpdates {
		reportStep("❓", "Skipping updates…")
		slog.Warn("skipping updates", "host", host, "hostname", hostname, "--skip-updates value", enrollCfg.SkipUpdates)
	} else {
		reportStep("⏳", "Applying updates…")
		if err := applyUpdates(ctx, host, hostname, deps); err != nil {
			slog.Error("failed to apply updates", "host", host, "hostname", hostname, "error", err)
			reportStep("⚠️", "Failed to apply updates…")
			// Non-fatal because router might not have internet access yet
		}
		reportStep("✅", "Updates applied successfully")
	}

	// Step 6: Export configuration (optional)
	if enrollCfg.SkipExport {
		reportStep("❓", "Skipping export…")
		slog.Warn("skipping export", "host", host, "hostname", hostname, "--skip-export value", enrollCfg.SkipExport)
	} else {
		reportStep("⏳", "Exporting configuration…")
		conn, err = exportConfiguration(ctx, host, hostname, enrollCfg, deps, conn)
		if err != nil {
			slog.Error("configuration export failed", "host", host, "hostname", hostname, "error", err)
			return fmt.Errorf("failed to export configuration: %w", err)
		}
		reportStep("✅", "Configuration successfully exported")
	}

	// Step 7: Apply post-enrollment script
	reportStep("⏳", "Applying post-enroll script…")
	if err := applyPostEnrollScript(conn, enrollCfg); err != nil {
		slog.Error("post-enroll script failed", "host", host, "hostname", hostname, "error", err)
		return fmt.Errorf("failed to apply post-enroll script: %w", err)
	}
	reportStep("✅", "Post-enroll script applied successfully")

	return nil
}

// connectToRouter establishes an SSH connection to the router
func connectToRouter(ctx context.Context, host string, hostname string, deps EnrollDependencies) (ssh.RunnerInterface, error) {
	slog.Debug("connecting to router", "host", host)
	conn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to connect to router", "host", host, "hostname", hostname, "error", err)
		return nil, fmt.Errorf("failed to connect to router: %w", err)
	}
	slog.Debug("successfully connected", "host", host, "hostname", hostname)
	return conn, nil
}

// applyPreEnrollScript applies the pre-enrollment configuration script
func applyPreEnrollScript(conn ssh.RunnerInterface, cfg EnrollConfig) error {
	hostname := cfg.Hostname
	slog.Debug("applying pre-enroll configuration file", "hostname", hostname)
	if err := applyConfigFile(conn, cfg.PreEnrollScript); err != nil {
		return fmt.Errorf("failed to apply pre-enroll configuration file: %w", err)
	}
	return nil
}

// applyUpdates applies system updates unless skipped
func applyUpdates(ctx context.Context, host string, hostname string, deps EnrollDependencies) error {
	slog.Debug("checking and applying updates", "host", host, "hostname", hostname)
	if err := deps.ApplyUpdatesFunc(ctx, host); err != nil {
		return fmt.Errorf("failed to apply updates: %w", err)
	}
	return nil
}

// exportConfiguration exports the router configuration and recreates SSH connection
func exportConfiguration(ctx context.Context, host string, hostname string, enrollCfg EnrollConfig, deps EnrollDependencies, conn ssh.RunnerInterface) (ssh.RunnerInterface, error) {
	slog.Debug("exporting final configuration", "host", host, "hostname", hostname)
	if err := deps.ExportConfigFunc(ctx, host, enrollCfg.OutputDir, false, hostname); err != nil {
		slog.Error("failed to export configuration", "host", host, "hostname", hostname, "error", err)
		return nil, fmt.Errorf("failed to export configuration: %w", err)
	}

	// Export closes its SSH connection, so we need to reconnect
	slog.Debug("recreating SSH connection after export", "host", host, "hostname", hostname)
	_ = conn.Close()
	newConn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to reconnect after export", "host", host, "hostname", hostname, "error", err)
		return nil, fmt.Errorf("failed to reconnect after export: %w", err)
	}
	slog.Debug("reconnected after export", "host", host, "hostname", hostname)
	return newConn, nil
}

// applyPostEnrollScript applies the post-enrollment configuration script
func applyPostEnrollScript(conn ssh.RunnerInterface, cfg EnrollConfig) error {
	hostname := cfg.Hostname
	slog.Debug("applying post-enroll configuration file", "hostname", hostname)
	if err := applyConfigFile(conn, cfg.PostEnrollScript); err != nil {
		return fmt.Errorf("failed to apply post-enroll configuration file: %w", err)
	}
	return nil
}

// applyConfigFile reads and executes RouterOS commands from a file
func applyConfigFile(conn ssh.RunnerInterface, filePath string) error {
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
func setRouterIdentity(conn ssh.RunnerInterface, hostname string) error {
	if hostname == "" {
		slog.Debug("skipping router identity set, hostname is empty", "hostname", hostname)
		return nil
	}
	cmd := fmt.Sprintf("/system identity set name=%s", hostname)
	slog.Debug("setting identity with command", "hostname", hostname, "command", cmd)
	_, err := conn.Run(cmd)
	if err != nil {
		slog.Error("failed to set router identity", "hostname", hostname, "error", err)
		return fmt.Errorf("failed to set identity: %w", err)
	}
	return nil
}

// updateHostKey captures the SSH host key for the first time or updates an existing one,
// without performing full enrollment.
func updateHostKey(ctx context.Context, host string, deps EnrollDependencies) (string, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled: %w", err)
	}

	slog.Info("starting host key update", "host", host)

	// Load existing host key info if it exists
	oldInfo, err := ssh.LoadHostKeyInfo(host)
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
	newInfo, err := ssh.LoadHostKeyInfo(host)
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

// deleteExistingEnrollment removes all enrollment artifacts for a host.
// It returns metadata describing what was removed so the caller can decide
// how to surface that information through the display system.
func deleteExistingEnrollment(host string, hostname string) (deleteExistingEnrollmentResult, error) {
	slog.Info("deleting existing enrollment artifacts", "host", host, "hostname", hostname)
	result := deleteExistingEnrollmentResult{}

	// Delete host key
	if ssh.HostKeyExists(host) {
		slog.Debug("deleting host key", "host", host, "hostname", hostname)
		if err := ssh.DeleteHostKey(host); err != nil {
			slog.Error("failed to delete host key", "host", host, "hostname", hostname, "error", err)
			return result, fmt.Errorf("failed to delete host key: %w", err)
		}
		result.hostKeyDeleted = true
	}

	// Delete config file
	parsedHost := ssh.ParseHost(host)
	configFile := fmt.Sprintf("%s.rsc", parsedHost.ShortName)
	if _, err := os.Stat(configFile); err == nil {
		slog.Debug("deleting config file", "host", host, "hostname", hostname, "file", configFile)
		if err := os.Remove(configFile); err != nil {
			slog.Error("failed to delete config file", "host", host, "hostname", hostname, "file", configFile, "error", err)
			return result, fmt.Errorf("failed to delete config file: %w", err)
		}
		result.configDeleted = configFile
	}

	slog.Info("existing enrollment artifacts deleted", "host", host, "hostname", hostname)
	return result, nil
}

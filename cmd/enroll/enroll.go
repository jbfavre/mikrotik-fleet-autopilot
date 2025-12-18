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
				Usage:       "Router hostname/identity to set (e.g., router1)",
				Destination: &hostname,
				Required:    true,
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := core.GetConfig(ctx)
			if err != nil {
				slog.Debug("failed to get global config", "error", err)
				return err
			}

			// Enroll command should only work with a single host
			if len(cfg.Hosts) != 1 {
				slog.Debug("enroll command requires exactly one host", "got", len(cfg.Hosts))
				return fmt.Errorf("enroll command requires exactly one host, got %d", len(cfg.Hosts))
			}

			host := cfg.Hosts[0]
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

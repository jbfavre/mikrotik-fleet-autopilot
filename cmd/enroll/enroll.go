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
				return err
			}

			// Enroll command should only work with a single host
			if len(cfg.Hosts) != 1 {
				return fmt.Errorf("enroll command requires exactly one host, got %d", len(cfg.Hosts))
			}

			host := cfg.Hosts[0]
			return enroll(ctx, host)
		},
	},
}

// enroll performs the enrollment workflow for a single router
func enroll(ctx context.Context, host string) error {
	fmt.Printf("🚀 Starting enrollment for router at %s\n", host)

	// Step 1: Connect to router
	slog.Info("Step 1: Connecting to router")
	conn, err := sshConnectionFactory(ctx, host)
	if err != nil {
		return fmt.Errorf("failed to connect to host %s: %w", host, err)
	}
	defer func() {
		_ = conn.Close()
	}()
	fmt.Printf("✅ Connected to %s\n", host)

	// Step 2: Apply pre-enroll configuration file
	slog.Info("Step 2: Applying pre-enroll configuration file")
	if err := applyConfigFile(conn, preEnrollScript); err != nil {
		return fmt.Errorf("failed to apply pre-enroll configuration file: %w", err)
	}
	fmt.Println("✅ Pre enroll configuration file applied")

	// Step 3: Set router identity
	slog.Info("Step 3: Setting router identity")
	if err := setRouterIdentity(conn, hostname); err != nil {
		return fmt.Errorf("failed to set router identity: %w", err)
	}
	fmt.Printf("✅ Router identity set to: %s\n", hostname)

	// Step 4: Apply updates (unless skipped)
	if !skipUpdates {
		slog.Info("Step 4: Checking and applying updates")
		fmt.Println("⏳ Checking for updates...")
		if err := applyUpdatesFunc(ctx, host); err != nil {
			slog.Warn("Failed to apply updates: " + err.Error())
			fmt.Printf("⚠️  Update check failed (non-fatal): %v\n", err)
		}
	} else {
		slog.Info("Step 4: Skipping updates")
		fmt.Println("⏭️  Skipping updates")
	}

	// Step 5: Export configuration (unless skipped)
	// Export properly manages its own SSH connection
	if !skipExport {
		slog.Info("Step 5: Exporting final configuration")
		fmt.Println("⏳ Exporting configuration...")
		if err := exportConfigFunc(ctx, host, outputDir, false); err != nil {
			slog.Warn("Failed to export configuration: " + err.Error())
			fmt.Printf("⚠️  Export failed (non-fatal): %v\n", err)
		} else {
			// Export closes its SSH connection, invalidating our SSH connection
			// Recreate the connection to ensure subsequent steps work properly
			slog.Info("Recreating SSH connection after export")
			_ = conn.Close()
			conn, err = sshConnectionFactory(ctx, host)
			if err != nil {
				return fmt.Errorf("failed to reconnect after export: %w", err)
			}
			fmt.Println("✅ Reconnected after export")
		}
	} else {
		slog.Info("Step 5: Skipping export")
		fmt.Println("⏭️  Skipping export")
	}

	// Step 6: Apply post-enroll configuration file
	slog.Info("Step 6: Applying post-enroll configuration file")
	if err := applyConfigFile(conn, postEnrollScript); err != nil {
		return fmt.Errorf("failed to apply post-enroll configuration file: %w", err)
	}
	fmt.Println("✅ Post enroll configuration file applied")

	fmt.Printf("\n🎉 Enrollment completed successfully for %s\n", host)
	return nil
}

// applyConfigFile reads and executes RouterOS commands from a file
func applyConfigFile(conn core.SshRunner, filePath string) error {
	// Read file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
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

		slog.Debug(fmt.Sprintf("Executing command (line %d): %s", lineNum, line))
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
	slog.Debug("Setting identity with command: " + cmd)
	_, err := conn.Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to set identity: %w", err)
	}
	return nil
}

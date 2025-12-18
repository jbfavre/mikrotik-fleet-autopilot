package export

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var showSensitive bool
var outputDir string

// sshConnectionFactory is the factory function for creating SSH connections
// This can be overridden in tests to inject mock SSH manager
var sshConnectionFactory = core.CreateConnection

var Command = []*cli.Command{
	{
		Name:  "export",
		Usage: "Export MikroTik router configuration",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "show-sensitive",
				Value:       false,
				Usage:       "Include sensitive information in the export",
				Destination: &showSensitive,
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

			// Iterate over all hosts
			var lastErr error
			for _, host := range cfg.Hosts {
				if err := export(ctx, host); err != nil {
					lastErr = err
					// Continue with other hosts even if one fails
				}
			}
			return lastErr
		},
	},
}

// ExportConfig is a public wrapper that exports configuration for a single host
// This function is intended to be called from other subcommands like enroll
func ExportConfig(ctx context.Context, host string, exportOutputDir string, exportShowSensitive bool) error {
	// Temporarily override package-level flags for programmatic calls
	originalOutputDir := outputDir
	originalShowSensitive := showSensitive
	outputDir = exportOutputDir
	showSensitive = exportShowSensitive
	defer func() {
		outputDir = originalOutputDir
		showSensitive = originalShowSensitive
	}()

	return export(ctx, host)
}

func export(ctx context.Context, host string) error {
	slog.Info("exporting configuration", "host", host)

	slog.Debug("initializing SSH connection", "host", host)
	conn, err := sshConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to create SSH connection", "host", host, "error", err)
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	sshCmd := "/export terse"
	if showSensitive {
		sshCmd += " show-sensitive"
	}
	slog.Debug("executing export command", "command", sshCmd, "show-sensitive", showSensitive)

	result, err := conn.Run(sshCmd)
	if err != nil {
		slog.Error("failed to export configuration", "host", host, "error", err)
		fmt.Printf("❌ %s: Export failed\n", host)
		return fmt.Errorf("failed to export configuration: %w", err)
	}

	// Clean up Windows line endings (CRLF -> LF)
	result = strings.ReplaceAll(result, "\r\n", "\n")

	// Generate output filename: remove domain suffix if present
	hostname := host
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}
	filename := fmt.Sprintf("%s.rsc", hostname)
	filepath := filepath.Join(outputDir, filename)

	slog.Debug("writing configuration", "file", filepath, "size", len(result))
	if err := os.WriteFile(filepath, []byte(result), 0644); err != nil {
		slog.Error("failed to write configuration file", "host", host, "file", filepath, "error", err)
		fmt.Printf("❌ %s: Export failed\n", host)
		return fmt.Errorf("failed to write configuration file: %w", err)
	}

	slog.Info("configuration exported successfully", "host", host, "file", filename)
	fmt.Printf("✅ %s: Configuration exported to %s\n", host, filename)
	return nil
}

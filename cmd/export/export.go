package export

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	core "jb.favre/mikrotik-fleet-autopilot/common/core"
	sshpkg "jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// Config holds all export configuration options
type Config struct {
	ShowSensitive bool
	OutputDir     string
}

// Dependencies holds injectable dependencies for testing
type Dependencies struct {
	SSHConnectionFactory func(context.Context, string) (sshpkg.Runner, error)
}

var Command = []*cli.Command{
	{
		Name:  "export",
		Usage: "Export MikroTik router configuration",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "show-sensitive",
				Value: false,
				Usage: "Include sensitive information in the export",
			},
			&cli.StringFlag{
				Name:  "output-dir",
				Value: ".",
				Usage: "Directory where to save the exported configuration",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			coreCfg, err := core.GetConfig(ctx)
			if err != nil {
				slog.Debug("failed to get global config", "error", err)
				return err
			}

			// Build export configuration from CLI flags
			exportCfg := Config{
				ShowSensitive: cmd.Bool("show-sensitive"),
				OutputDir:     cmd.String("output-dir"),
			}

			deps := Dependencies{
				SSHConnectionFactory: core.CreateConnection,
			}

			// Iterate over all hosts
			var lastErr error
			for _, host := range coreCfg.Hosts {
				if err := export(ctx, host, "", exportCfg, deps); err != nil { // Empty string = derive from host
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
func ExportConfig(ctx context.Context, host string, exportOutputDir string, exportShowSensitive bool, preferredFilename string) error {
	cfg := Config{
		ShowSensitive: exportShowSensitive,
		OutputDir:     exportOutputDir,
	}
	deps := Dependencies{
		SSHConnectionFactory: core.CreateConnection,
	}
	return export(ctx, host, preferredFilename, cfg, deps)
}

func export(ctx context.Context, host string, preferredFilename string, cfg Config, deps Dependencies) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	slog.Info("exporting configuration", "host", host)

	slog.Debug("initializing SSH connection", "host", host)
	conn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		slog.Error("failed to create SSH connection", "host", host, "error", err)
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	sshCmd := "/export terse"
	if cfg.ShowSensitive {
		sshCmd += " show-sensitive"
	}
	slog.Debug("executing export command", "command", sshCmd, "show-sensitive", cfg.ShowSensitive)

	result, err := conn.Run(sshCmd)
	if err != nil {
		slog.Error("failed to export configuration", "host", host, "error", err)
		fmt.Printf("❌ %s: Export failed\n", host)
		return fmt.Errorf("failed to export configuration: %w", err)
	}

	// Clean up Windows line endings (CRLF -> LF)
	result = strings.ReplaceAll(result, "\r\n", "\n")

	// Generate output filename
	var filename string
	if preferredFilename != "" {
		// Use provided name (from enroll's --hostname)
		filename = fmt.Sprintf("%s.rsc", preferredFilename)
	} else {
		// Derive from host using HostInfo
		hostInfo := sshpkg.ParseHost(host)
		filename = fmt.Sprintf("%s.rsc", hostInfo.ShortName)
	}
	filepath := filepath.Join(cfg.OutputDir, filename)

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

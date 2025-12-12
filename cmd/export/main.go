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
// This can be overridden in tests to inject mock connections
var sshConnectionFactory = func(host, user, password string) (core.SshRunner, error) {
	return core.NewSsh(host, user, password)
}

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
				return err
			}
			return exportConfig(cfg)
		},
	},
}

func init() {}

func exportConfig(cfg *core.Config) error {
	// SSH init
	slog.Info("Initializing SSH connection")
	conn, err := sshConnectionFactory(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer func() {
		_ = conn.Close() // Error logging handled inside Close()
	}()

	// Build Mikrotik command line
	sshCmd := "/export terse"
	if showSensitive {
		sshCmd += " show-sensitive"
	}
	slog.Debug("SSH cmd is " + sshCmd)

	// Export configuration
	slog.Info("Exporting router configuration")
	result, err := conn.Run(sshCmd)
	if err != nil {
		fmt.Printf("⚠️  %s export failed: %v\n", cfg.Host, err)
		return fmt.Errorf("failed to export configuration: %w", err)
	}

	// Clean up Windows line endings (CRLF -> LF)
	result = strings.ReplaceAll(result, "\r\n", "\n")

	// Generate output filename: remove domain suffix if present
	hostname := cfg.Host
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}
	filename := fmt.Sprintf("%s.rsc", hostname)
	filepath := filepath.Join(outputDir, filename)

	// Write to file
	slog.Debug("Writing configuration to " + filepath)
	if err := os.WriteFile(filepath, []byte(result), 0644); err != nil {
		fmt.Printf("⚠️  %s export failed: could not write file %s\n", cfg.Host, filepath)
		return fmt.Errorf("failed to write configuration file: %w", err)
	}

	// Success message
	fmt.Printf("✅ %s configuration exported to %s\n", cfg.Host, filename)
	return nil
}

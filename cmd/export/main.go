package export

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var showSensitive bool
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := core.GetConfig(ctx)
			if err != nil {
				return err
			}
			return export(ctx, cmd, cfg)
		},
	},
}

func export(ctx context.Context, cmd *cli.Command, cfg *core.Config) error {
	slog.Info("Starting export command")
	// Build Mikrotik command line
	sshCmd := "/export terse"
	if showSensitive {
		sshCmd += " show-sensitive"
	}
	slog.Debug("SSH cmd is " + sshCmd)

	// SSH init
	conn, err := core.NewSsh(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer conn.Close()

	// Ping router to check it's up
	// Run SSH command to export configuration
	// Store exported configuration into a file formatted as <router_name>.rsc
	return nil
}

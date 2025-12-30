package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/cmd/enroll"
	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

func main() {
	var globalConfig core.Config
	var hosts string
	// SSH credentials - kept separate from config for security
	var sshPassword string
	var sshPassphrase string

	cmd := buildCommand(&globalConfig, &hosts, &sshPassword, &sshPassphrase)

	if err := cmd.Run(context.WithValue(context.Background(), core.ConfigKey, &globalConfig), os.Args); err != nil {
		slog.Error("command failed", "error", err)
	}
}

// buildCommand creates and configures the CLI command structure.
// This function is extracted to make the CLI testable.
func buildCommand(globalConfig *core.Config, hosts, sshPassword, sshPassphrase *string) *cli.Command {
	return &cli.Command{
		Name:    "mikrotik-fleet-autopilot",
		Version: "0.1.0",
		Authors: []any{
			"Jean Baptiste Favre",
		},
		Usage:                 "Automate. Control. Scale. Your MikroTik fleet on autopilot.",
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "host",
				Aliases:     []string{"H"},
				Value:       "",
				Usage:       "MikroTik router hostname or IP address (comma-separated for multiple routers). If not provided, will auto-discover from router*.rsc files in current directory",
				Destination: hosts,
			},
			&cli.StringFlag{
				Name:        "ssh-user",
				Aliases:     []string{"u"},
				Category:    "ssh",
				Value:       "admin",
				Usage:       "MikroTik router SSH username",
				Destination: &globalConfig.User,
			},
			&cli.StringFlag{
				Name:        "ssh-password",
				Aliases:     []string{"p"},
				Category:    "ssh",
				Value:       "",
				Usage:       "MikroTik router SSH password",
				Destination: sshPassword,
			},
			&cli.StringFlag{
				Name:        "ssh-passphrase",
				Aliases:     []string{"P"},
				Category:    "ssh",
				Value:       "",
				Usage:       "User private SSH key passphrase",
				Destination: sshPassphrase,
			},
			&cli.BoolFlag{
				Name:        "skip-hostkey-check",
				Category:    "ssh",
				Value:       false,
				Usage:       "⚠️  INSECURE: Skip host key verification (for testing only)",
				Destination: &globalConfig.SkipHostKeyCheck,
			},
			&cli.BoolFlag{
				Name:        "debug",
				Aliases:     []string{"d"},
				Category:    "log",
				Usage:       "Enable debug logging",
				Destination: &globalConfig.Debug,
			},
		},
		Commands: append(append(export.Command, updates.Command...), enroll.Command...),
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Set log level
			core.SetupLogging(slog.LevelWarn)
			if globalConfig.Debug {
				core.SetupLogging(slog.LevelDebug)
			}
			slog.Info("Starting global")

			// Check if a subcommand was provided
			// If not, the help will be shown automatically by urfave/cli
			if cmd.Args().Len() > 0 {
				slog.Debug("cmd args", "args", cmd.Args())
				// Setup hosts
				if *hosts != "" {
					// Split comma-separated hosts
					globalConfig.Hosts = core.ParseHosts(*hosts)
				} else {
					// Auto-discover routers
					routers, err := core.DiscoverHosts()
					if err != nil {
						return ctx, fmt.Errorf("failed to discover routers: %w", err)
					}
					globalConfig.Hosts = routers
					slog.Info("auto-discovered routers", "count", len(routers), "routers", routers)
				}

				if len(globalConfig.Hosts) == 0 {
					slog.Error("no routers specified or discovered")
					return ctx, fmt.Errorf("no routers specified or discovered")
				}
			}
			// Create SSH manager with credentials (credentials stay encapsulated)
			sshManager := core.NewSshManager(globalConfig.User, *sshPassword, *sshPassphrase)

			// Make global config (without credentials) and SSH manager available in context
			ctx = context.WithValue(ctx, core.ConfigKey, globalConfig)
			ctx = context.WithValue(ctx, core.SshManagerKey, sshManager)
			slog.Debug("global config available in context", "config", *globalConfig)
			slog.Info("starting subcommand", "subcommand", cmd.Args().Get(0))
			return ctx, nil
		},
	}
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/cmd/discover"
	"jb.favre/mikrotik-fleet-autopilot/cmd/enroll"
	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
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
				Usage:       "Comma-separated list of MikroTik hosts. Mutually exclusive with --mndp",
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
			&cli.IntFlag{
				Name:        "max-concurrent-hosts",
				Value:       0,
				Usage:       "Maximum number of hosts to process in parallel (0 = auto-detect, 1 = sequential, >=2 = parallel with that cap)",
				Destination: &globalConfig.MaxConcurrentHosts,
			},
			&cli.BoolFlag{
				Name:        "buffered-output",
				Value:       false,
				Usage:       "Force buffered host progress output (deterministic final flush). By default, live display is preferred on TTY unless --debug is enabled",
				Destination: &globalConfig.BufferedOutput,
			},
			&cli.StringFlag{
				Name:        "interface",
				Aliases:     []string{"i"},
				Category:    "discovery",
				Usage:       "Network interface to use for MNDP discovery (default: all non-loopback interfaces). Mutually exclusive with --host",
				Destination: &globalConfig.Interface,
			},
			&cli.BoolFlag{
				Name:        "mndp",
				Category:    "discovery",
				Usage:       "Dynamically discover MikroTik devices via MNDP. Mutually exclusive with --host",
				Destination: &globalConfig.UseMNDP,
			},
			&cli.StringFlag{
				Name:     "mndp-timeout",
				Category: "discovery",
				Value:    "15s",
				Usage:    "How long to listen for MNDP responses (e.g. 15s, 1m). Only used with --mndp",
			},
		},
		Commands: append(append(append(export.Command, updates.Command...), enroll.Command...), discover.Command...),
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Set log level once at startup.
			logLevel := slog.LevelWarn
			if globalConfig.Debug {
				logLevel = slog.LevelDebug
			}
			core.SetupLogging(logLevel)
			slog.Info("Starting global")

			effectiveMaxConcurrent, err := core.ResolveMaxConcurrentHosts(globalConfig.MaxConcurrentHosts)
			if err != nil {
				return ctx, err
			}
			slog.Debug("resolved max concurrent hosts", "configured", globalConfig.MaxConcurrentHosts, "effective", effectiveMaxConcurrent)
			globalConfig.EffectiveMaxConcurrent = effectiveMaxConcurrent
			globalConfig.PreferLiveMode = !globalConfig.BufferedOutput

			if raw := cmd.String("mndp-timeout"); raw != "" {
				d, err := time.ParseDuration(raw)
				if err != nil {
					return ctx, fmt.Errorf("invalid --mndp-timeout value %q: %w", raw, err)
				}
				globalConfig.MNDPTimeout = d
			}

			// Check if a subcommand was provided
			// If not, the help will be shown automatically by urfave/cli
			if cmd.Args().Len() > 0 {
				slog.Debug("command line arguments", "args", cmd.Args())

				// Mutual exclusivity guard
				if *hosts != "" && globalConfig.UseMNDP {
					return ctx, fmt.Errorf("--host and --mndp are mutually exclusive: use --mndp for dynamic discovery or --host to target specific devices")
				}

				// Setup hosts
				if *hosts != "" {
					// Split comma-separated hosts
					globalConfig.Hosts = core.ParseHosts(*hosts)
				} else if !globalConfig.UseMNDP {
					// Auto-discover routers
					routers, err := core.DiscoverHosts()
					if err != nil {
						return ctx, fmt.Errorf("failed to discover routers: %w", err)
					}
					globalConfig.Hosts = routers
					slog.Info("auto-discovered routers", "count", len(routers), "routers", routers)
				}
				// When --mndp is set, Hosts stays empty here; discover subcommand populates it.

				if len(globalConfig.Hosts) == 0 && !globalConfig.UseMNDP {
					slog.Error("no routers specified or discovered")
					return ctx, fmt.Errorf("no routers specified or discovered")
				}
			}
			// Create SSH manager with credentials (credentials stay encapsulated)
			sshManager := ssh.NewSshManager(globalConfig.User, *sshPassword, *sshPassphrase)

			// Make global config (without credentials) and SSH manager available in context
			ctx = context.WithValue(ctx, core.ConfigKey, globalConfig)
			ctx = context.WithValue(ctx, core.SshManagerKey, sshManager)
			slog.Debug("global config available in context", "config", *globalConfig)
			slog.Info("starting subcommand", "subcommand", cmd.Args().Get(0))
			return ctx, nil
		},
	}
}

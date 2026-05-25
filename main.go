package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/cmd/enroll"
	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/topology"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/discover"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

func main() {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	if err := cmd.Run(context.WithValue(context.Background(), core.ConfigKey, &globalConfig), os.Args); err != nil {
		slog.Error("command failed", "error", err)
	}
}

// buildCommand creates and configures the CLI command structure.
// This function is extracted to make the CLI testable.
func buildCommand(globalConfig *core.Config, hosts *string) *cli.Command {
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
				Usage:       "Comma-separated list of MikroTik hosts. Mutually exclusive with --mndp. If omitted, hosts resolve via topology discovery when --mndp is set, otherwise from router*.rsc files",
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
				Name:     "ssh-password",
				Aliases:  []string{"p"},
				Category: "ssh",
				Usage:    "MikroTik router SSH password",
			},
			&cli.StringFlag{
				Name:     "ssh-passphrase",
				Aliases:  []string{"P"},
				Category: "ssh",
				Usage:    "User private SSH key passphrase",
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
				Usage:       "Network interface to use for MNDP discovery (default: all non-loopback interfaces). Only used with --mndp",
				Destination: &globalConfig.Interface,
			},
			&cli.BoolFlag{
				Name:        "mndp",
				Category:    "discovery",
				Usage:       "Enable topology discovery host resolution via MNDP/LLDP. Mutually exclusive with --host",
				Destination: &globalConfig.UseMNDP,
			},
			&cli.StringFlag{
				Name:     "mndp-timeout",
				Category: "discovery",
				Value:    "15s",
				Usage:    "How long to listen for MNDP responses (e.g. 15s, 1m). Only used with --mndp",
			},
		},
		Commands: append(append(append(export.Command, updates.Command...), enroll.Command...), topology.Command...),
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			var passwordPtr *string
			if cmd.IsSet("ssh-password") {
				v := cmd.String("ssh-password")
				passwordPtr = &v
			}
			var passphrasePtr *string
			if cmd.IsSet("ssh-passphrase") {
				v := cmd.String("ssh-passphrase")
				passphrasePtr = &v
			}
			params := BeforeHookParams{
				HasSubcommand:  cmd.Args().Len() > 0,
				Subcommand:     cmd.Args().Get(0),
				HostsRaw:       *hosts,
				MNDPTimeoutRaw: cmd.String("mndp-timeout"),
				Password:       passwordPtr,
				Passphrase:     passphrasePtr,
			}
			return runBeforeHook(ctx, globalConfig, params, defaultBeforeHookDeps())
		},
	}
}

// BeforeHookParams holds CLI flag values needed by the Before hook logic.
// Extracted from *cli.Command so the logic can be tested without a real CLI context.
type BeforeHookParams struct {
	HasSubcommand  bool
	Subcommand     string
	HostsRaw       string  // raw value of the --host flag
	MNDPTimeoutRaw string  // raw value of --mndp-timeout, e.g. "15s"
	Password       *string // nil when --ssh-password was not set
	Passphrase     *string // nil when --ssh-passphrase was not set
}

// BeforeHookDeps holds injectable side-effectful functions for testing.
type BeforeHookDeps struct {
	// ResolveHostsDiscoveryFirst performs MNDP/LLDP discovery with local fallback.
	ResolveHostsDiscoveryFirst func(ctx context.Context, cfg discover.Config, deps discover.Dependencies) ([]string, error)
	// DiscoverLocalHosts resolves hosts from local router*.rsc files.
	DiscoverLocalHosts func() ([]string, error)
}

// defaultBeforeHookDeps returns the production implementations of BeforeHookDeps.
func defaultBeforeHookDeps() BeforeHookDeps {
	return BeforeHookDeps{
		ResolveHostsDiscoveryFirst: discover.ResolveHostsDiscoveryFirst,
		DiscoverLocalHosts:         core.DiscoverHosts,
	}
}

// runBeforeHook contains all the Before hook business logic.
// It is called by the thin Before closure in buildCommand, which is responsible only
// for extracting BeforeHookParams from *cli.Command.
func runBeforeHook(ctx context.Context, globalConfig *core.Config, params BeforeHookParams, deps BeforeHookDeps) (context.Context, error) {
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

	if params.MNDPTimeoutRaw != "" {
		d, err := time.ParseDuration(params.MNDPTimeoutRaw)
		if err != nil {
			return ctx, fmt.Errorf("invalid --mndp-timeout value %q: %w", params.MNDPTimeoutRaw, err)
		}
		if d <= 0 {
			return ctx, fmt.Errorf("invalid --mndp-timeout value %q: must be greater than 0", params.MNDPTimeoutRaw)
		}
		globalConfig.MNDPTimeout = d
	} else {
		return ctx, fmt.Errorf("invalid --mndp-timeout value %q: cannot be empty", params.MNDPTimeoutRaw)
	}

	if params.HasSubcommand {
		slog.Debug("command line arguments", "subcommand", params.Subcommand)

		// Mutual exclusivity guard
		if params.HostsRaw != "" && globalConfig.UseMNDP {
			return ctx, fmt.Errorf("--host and --mndp are mutually exclusive: use --mndp for dynamic discovery or --host to target specific devices")
		}

		// Create SSH manager with credentials (credentials stay encapsulated)
		sshManager := ssh.NewSshManager(globalConfig.User, params.Password, params.Passphrase)
		ctx = context.WithValue(ctx, core.SshManagerKey, sshManager)

		// Setup hosts
		if params.HostsRaw != "" {
			globalConfig.Hosts = core.ParseHosts(params.HostsRaw)
		} else {
			switch {
			case params.Subcommand == "topology":
				if !globalConfig.UseMNDP {
					return ctx, fmt.Errorf("--mndp is required with the topology subcommand")
				}
				// topology subcommand performs strict discovery itself.
			case globalConfig.UseMNDP:
				routers, err := deps.ResolveHostsDiscoveryFirst(ctx, discover.Config{
					UseMNDP:     true,
					Interface:   globalConfig.Interface,
					MNDPTimeout: globalConfig.MNDPTimeout,
				}, discover.Dependencies{})
				if err != nil {
					return ctx, fmt.Errorf("failed to resolve hosts from topology discovery: %w", err)
				}
				globalConfig.Hosts = routers
				slog.Info("resolved routers from topology discovery", "count", len(routers), "routers", routers)
			default:
				routers, err := deps.DiscoverLocalHosts()
				if err != nil {
					return ctx, fmt.Errorf("failed to discover routers: %w", err)
				}
				globalConfig.Hosts = routers
				slog.Info("auto-discovered routers", "count", len(routers), "routers", routers)
			}
		}

		if len(globalConfig.Hosts) == 0 && params.Subcommand != "topology" {
			slog.Error("no routers specified or discovered")
			return ctx, fmt.Errorf("no routers specified or discovered")
		}
	}

	ctx = context.WithValue(ctx, core.ConfigKey, globalConfig)
	slog.Debug("global config available in context", "config", *globalConfig)
	slog.Info("starting subcommand", "subcommand", params.Subcommand)
	return ctx, nil
}

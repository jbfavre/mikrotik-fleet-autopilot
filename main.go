package main

import (
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

// parseHostsFlag parses a comma-separated list of hosts and returns a slice of trimmed host strings.
// Empty strings and whitespace-only entries are filtered out.
func parseHostsFlag(hostsFlag string) []string {
	if hostsFlag == "" {
		return []string{}
	}

	hosts := []string{}
	for h := range strings.SplitSeq(hostsFlag, ",") {
		trimmed := strings.TrimSpace(h)
		if trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	return hosts
}

func main() {
	var globalConfig core.Config
	var hostsFlag string
	cmd := &cli.Command{
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
				Value:       "",
				Usage:       "MikroTik router hostname or IP address (comma-separated for multiple routers). If not provided, will auto-discover from router*.rsc files in current directory",
				Destination: &hostsFlag,
			},
			&cli.StringFlag{
				Name:        "user",
				Value:       "admin",
				Usage:       "MikroTik router username",
				Destination: &globalConfig.User,
			},
			&cli.StringFlag{
				Name:        "password",
				Value:       "",
				Usage:       "MikroTik router password",
				Destination: &globalConfig.Password,
			},
			&cli.BoolFlag{
				Name:        "debug",
				Usage:       "Enable debug logging",
				Destination: &globalConfig.Debug,
			},
		},
		Commands: append(export.Command, updates.Command...),
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Set log level
			core.SetupLogging(slog.LevelWarn)
			if globalConfig.Debug {
				core.SetupLogging(slog.LevelDebug)
			}
			slog.Info("Starting global")

			// Determine hosts to use
			if hostsFlag != "" {
				// Split comma-separated hosts
				globalConfig.Hosts = parseHostsFlag(hostsFlag)
			} else {
				// Auto-discover routers
				routers, err := core.DiscoverRouters()
				if err != nil {
					return ctx, fmt.Errorf("failed to discover routers: %w", err)
				}
				globalConfig.Hosts = routers
				slog.Info(fmt.Sprintf("Auto-discovered %d router(s): %v", len(routers), routers))
			}

			if len(globalConfig.Hosts) == 0 {
				return ctx, fmt.Errorf("no routers specified or discovered")
			}

			// Make global config available in context
			ctx = context.WithValue(ctx, core.ConfigKey, &globalConfig)
			slog.Debug("globalConfig is available in context with value: " + fmt.Sprintf("%+v", globalConfig))
			slog.Info("Starting " + cmd.Args().Get(0) + " subcommand")
			return ctx, nil
		},
	}

	/*
		// Log init
		log.SetFlags(log.LstdFlags | log.Lshortfile)
		log.Printf("%+v", config)
		// SSH init
		if &config.Password == "" {
			&config.Password = getPassword("Enter Mikrotik password: ")
		}
		sshClient, err := NewSsh(fmt.Sprintf("%v:22", &config.Host), &config.User, &config.Password)
		if err != nil {
			log.Fatal(err)
		}
		defer sshClient.Close()
	*/
	if err := cmd.Run(context.WithValue(context.Background(), core.ConfigKey, &globalConfig), os.Args); err != nil {
		slog.Error("command failed: " + err.Error())
	}
}

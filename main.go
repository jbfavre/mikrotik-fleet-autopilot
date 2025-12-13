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

// parsehosts parses a comma-separated list of hosts and returns a slice of trimmed host strings.
// Empty strings and whitespace-only entries are filtered out.
func parseHosts(hosts string) []string {
	if hosts == "" {
		return []string{}
	}

	hostsList := []string{}
	for h := range strings.SplitSeq(hosts, ",") {
		trimmed := strings.TrimSpace(h)
		if trimmed != "" {
			hostsList = append(hostsList, trimmed)
		}
	}
	return hostsList
}

func main() {
	var globalConfig core.Config
	var hosts string
	// SSH credentials - kept separate from config for security
	var sshPassword string
	var sshPassphrase string
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
				Aliases:     []string{"H"},
				Value:       "",
				Usage:       "MikroTik router hostname or IP address (comma-separated for multiple routers). If not provided, will auto-discover from router*.rsc files in current directory",
				Destination: &hosts,
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
				Destination: &sshPassword,
			},
			&cli.StringFlag{
				Name:        "ssh-passphrase",
				Aliases:     []string{"P"},
				Category:    "ssh",
				Value:       "",
				Usage:       "User private SSH key passphrase",
				Destination: &sshPassphrase,
			},
			&cli.BoolFlag{
				Name:        "debug",
				Aliases:     []string{"d"},
				Category:    "log",
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

			// Binary called with a subcommand.
			// If not, do not discover routers
			if len(os.Args) > 1 {
				/* // Setup SSH
				sshClient, err := core.NewSsh(host, globalConfig.User, globalConfig.Password, globalConfig.Passphrase)
				if err != nil {
					slog.Error("error when setting up SSH: " + fmt.Sprintf("%v", err.Error))
				} */
				// Setup hosts
				if hosts != "" {
					// Split comma-separated hosts
					globalConfig.Hosts = parseHosts(hosts)
				} else {
					// Auto-discover routers
					routers, err := core.DiscoverHosts()
					if err != nil {
						return ctx, fmt.Errorf("failed to discover routers: %w", err)
					}
					globalConfig.Hosts = routers
					slog.Info(fmt.Sprintf("Auto-discovered %d router(s): %v", len(routers), routers))
				}

				if len(globalConfig.Hosts) == 0 {
					return ctx, fmt.Errorf("no routers specified or discovered")
				}
			}
			// Create SSH manager with credentials (credentials stay encapsulated)
			sshManager := core.NewSshManager(globalConfig.User, sshPassword, sshPassphrase)

			// Make global config (without credentials) and SSH manager available in context
			ctx = context.WithValue(ctx, core.ConfigKey, &globalConfig)
			ctx = context.WithValue(ctx, core.SshManagerKey, sshManager)
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

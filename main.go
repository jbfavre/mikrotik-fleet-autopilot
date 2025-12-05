package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

func main() {
	var globalConfig core.Config
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
				Value:       "172.16.0.1",
				Usage:       "MikroTik router hostname or IP address",
				Destination: &globalConfig.Host,
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
			if globalConfig.Debug {
				core.SetupLogging(slog.LevelDebug)
			}
			slog.Info("Starting global")
			// Make global config available in context
			ctx = context.WithValue(ctx, "config", &globalConfig)
			slog.Debug("globalConfig is available in context with value: " + fmt.Sprintf("%+v", globalConfig))
			slog.Info("Starting " + cmd.Args().Get(0) + " command")
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
	if err := cmd.Run(context.WithValue(context.Background(), "config", &globalConfig), os.Args); err != nil {
		slog.Error("command failed: " + err.Error())
	}
}

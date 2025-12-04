package updates

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var applyUpdates bool
var Command = []*cli.Command{
	{
		Name:  "updates",
		Usage: "Manages MikroTik router updates",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "apply-updates",
				Value:       false,
				Usage:       "Update router packages to the latest version available",
				Destination: &applyUpdates,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := core.GetConfig(ctx)
			if err != nil {
				return err
			}
			return updates(ctx, cmd, cfg)
		},
	},
}

func updates(ctx context.Context, cmd *cli.Command, cfg *core.Config) error {
	slog.Info("Starting updates command")
	sshCmd := "/system/package/update"
	if applyUpdates {
		sshCmd += "/install"
	} else {
		sshCmd += "/check-for-updates"
	}
	slog.Debug("SSH cmd is " + sshCmd)

	// SSH init
	conn, err := core.NewSsh(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer conn.Close()

	// Ping router to check it's up
	// Run SSH command to check for updates
	// If an update is available AND apply is selected,
	// 		Run SSH command to check partitions
	// 		If partition is available
	// 			Backup current running configuration on the backup partition
	// 		Run SSH command to apply updates
	// 		Ping router to check it's back up
	// 		Run SSH command to verify Routerboard status
	// 		Run SSH command to reboot if needed
	// 		Ping router to check it's back up
	return nil
}

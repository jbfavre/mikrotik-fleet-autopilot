package partitions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var create string
var Command = []*cli.Command{
	{
		Name:  "partitions",
		Usage: "Manages MikroTik router partitions",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "create",
				Value:       "backup",
				Usage:       "Create a new partition on the router",
				Destination: &create,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return partitions(ctx, cmd, ctx.Value("config").(*core.Config))
		},
	},
}

func init() {}

func partitions(ctx context.Context, cmd *cli.Command, cfg *core.Config) error {
	// SSH init
	conn, err := core.NewSsh(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer conn.Close()

	sshCmd := "/partitions"
	if create != "" {
		sshCmd += "/install"
	} else {
		sshCmd += "/check-for-updates"
	}
	slog.Debug("SSH cmd is " + sshCmd)

	// Ping router to check it's up
	// Run SSH command to check for existing partitions
	return nil
}

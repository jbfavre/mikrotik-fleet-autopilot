package partitions

import (
	"context"
	"log"

	"github.com/urfave/cli/v3"
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
			return partitions(ctx, cmd)
		},
	},
}

func partitions(ctx context.Context, cmd *cli.Command) error {
	sshCmd := "/partitions"
	if create != "" {
		sshCmd += "/install"
	} else {
		sshCmd += "/check-for-updates"
	}
	log.Printf("SSH cmd is %s\n", sshCmd)
	// Ping router to check it's up
	// Run SSH command to check for existing partitions
	return nil
}

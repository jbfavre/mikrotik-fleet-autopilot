package updates

import (
	"context"
	"log"

	"github.com/urfave/cli/v3"
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
			return updates(ctx, cmd)
		},
	},
}

func updates(ctx context.Context, cmd *cli.Command) error {
	sshCmd := "/system/package/update"
	if applyUpdates {
		sshCmd += "/install"
	} else {
		sshCmd += "/check-for-updates"
	}
	log.Printf("SSH cmd is %s\n", sshCmd)
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

package topology

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/discover"
	"jb.favre/mikrotik-fleet-autopilot/common/mndp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

var Command = []*cli.Command{
	{
		Name:  "topology",
		Usage: "Display discovered network topology graph",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "connected-to",
				Usage: "Show the local mfa computer as connected to a device identity or source host already present in the topology graph",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runTopologyForHosts(ctx, os.Stdout, cmd.String("connected-to"))
		},
	},
}

var createSSHConnection = ssh.CreateConnection

// listenMNDP is the MNDP listener function; injectable for testing.
var listenMNDP = mndp.Listen

func runTopologyForHosts(ctx context.Context, out io.Writer, connectedTo string) error {
	cfg, err := core.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	topo, err := discover.Build(ctx, cfg.Hosts, discover.Config{
		UseMNDP:     cfg.UseMNDP,
		Interface:   cfg.Interface,
		MNDPTimeout: cfg.MNDPTimeout,
	}, discover.Dependencies{
		CreateSSHConnection: createSSHConnection,
		ListenMNDP:          listenMNDP,
	})
	if err != nil {
		return fmt.Errorf("discovery failed: %w", err)
	}

	return outputTopology(out, topo, connectedTo)
}

type topology = discover.Topology

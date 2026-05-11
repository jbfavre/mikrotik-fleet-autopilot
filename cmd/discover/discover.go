package discover

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
	"jb.favre/mikrotik-fleet-autopilot/common/mndp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

var Command = []*cli.Command{
	{
		Name:  "discover",
		Usage: "Discover LLDP network topology across all routers",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "connected-to",
				Usage: "Show the local mfa computer as connected to a device identity or source host already present in the topology graph",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runDiscoverForHosts(ctx, os.Stdout, cmd.String("connected-to"))
		},
	},
}

var createSSHConnection = ssh.CreateConnection

// listenMNDP is the MNDP listener function; injectable for testing.
var listenMNDP = mndp.Listen

func runDiscoverForHosts(ctx context.Context, out io.Writer, connectedTo string) error {
	cfg, err := core.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	hosts := cfg.Hosts
	var mndpByIP map[string]*mndp.Device
	var ipToIdentity map[string]string

	if cfg.UseMNDP {
		slog.Info("starting MNDP discovery", "interface", cfg.Interface, "timeout", cfg.MNDPTimeout)
		devices, err := listenMNDP(ctx, cfg.Interface, cfg.MNDPTimeout)
		if err != nil {
			slog.Warn("MNDP discovery failed", "error", err)
			// non-fatal: continue with empty host list (will error below)
		}
		mndpByIP = make(map[string]*mndp.Device, len(devices))
		ipToIdentity = make(map[string]string, len(devices))
		for _, d := range devices {
			if d.IPv4Address != "" {
				mndpByIP[d.IPv4Address] = d
				ipToIdentity[d.IPv4Address] = d.Identity
			}
		}
		// Build ordered host list from MNDP results (already sorted by Identity in listener)
		for _, d := range devices {
			if d.IPv4Address != "" {
				hosts = append(hosts, d.IPv4Address)
			}
		}
		slog.Info("MNDP discovery complete", "devices", len(devices), "addressable", len(hosts))
	}

	if len(hosts) == 0 {
		return fmt.Errorf("no hosts configured for discovery")
	}

	slog.Info("starting LLDP discovery", "hosts", len(hosts))

	topo, err := discoverTopology(ctx, hosts)
	if err != nil {
		return fmt.Errorf("discovery failed: %w", err)
	}
	topo.mndpByIP = mndpByIP
	topo.ipToIdentity = ipToIdentity

	totalNeighbors := 0
	for _, result := range topo.results {
		totalNeighbors += len(result.Neighbors)
	}
	slog.Info("discovery complete",
		"hosts_ok", len(topo.results),
		"hosts_err", len(topo.errors),
		"total_neighbors", totalNeighbors,
	)

	return outputTopology(out, topo, connectedTo)
}

// topology holds discovery results indexed by source host
type topology struct {
	orderedHosts []string
	results      map[string]*lldp.ParseResult
	errors       map[string]error
	mndpByIP     map[string]*mndp.Device // IPv4Address → Device
	ipToIdentity map[string]string       // IPv4Address → MNDP Identity
}

func discoverTopology(ctx context.Context, hosts []string) (*topology, error) {
	topo := &topology{
		orderedHosts: make([]string, 0, len(hosts)),
		results:      make(map[string]*lldp.ParseResult),
		errors:       make(map[string]error),
	}

	for _, host := range hosts {
		topo.orderedHosts = append(topo.orderedHosts, host)
		slog.Info("connecting to host", "host", host)

		// Connect
		conn, err := createSSHConnection(ctx, host)
		if err != nil {
			slog.Warn("connection failed", "host", host, "error", err)
			topo.errors[host] = fmt.Errorf("connection failed: %w", err)
			continue
		}
		slog.Info("connected, running LLDP query", "host", host)

		// Run command
		output, err := conn.Run("/ip/neighbor/print detail where discovered-by~\"lldp\"")
		if err != nil {
			slog.Warn("command failed", "host", host, "error", err)
			topo.errors[host] = fmt.Errorf("command failed: %w", err)
			_ = conn.Close()
			continue
		}

		// Parse
		result, err := lldp.ParseNeighbors(output)
		if err != nil {
			slog.Warn("parse failed", "host", host, "error", err)
			topo.errors[host] = fmt.Errorf("parse failed: %w", err)
			_ = conn.Close()
			continue
		}

		if len(result.Warnings) > 0 {
			for _, warn := range result.Warnings {
				slog.Debug("parse warning", "host", host, "warning", warn)
			}
		}

		topo.results[host] = result
		slog.Info("discovered neighbors", "host", host, "count", len(result.Neighbors))

		_ = conn.Close()
	}

	return topo, nil
}

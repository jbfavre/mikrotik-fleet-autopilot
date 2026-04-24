package discover

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

var Command = &cli.Command{
	Name:  "discover",
	Usage: "Discover LLDP network topology across all routers",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return discoverAction(ctx)
	},
}

func discoverAction(ctx context.Context) error {
	// Get global config
	cfg, err := core.GetConfig(ctx)
	if err != nil {
		slog.Error("failed to get config", "error", err)
		return fmt.Errorf("failed to get config: %w", err)
	}

	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("no hosts configured for discovery")
	}

	slog.Info("starting LLDP discovery", "hosts", len(cfg.Hosts))

	// Discover neighbors from all hosts
	topology, err := discoverTopology(ctx, cfg.Hosts)
	if err != nil {
		slog.Error("discovery failed", "error", err)
		return fmt.Errorf("discovery failed: %w", err)
	}

	// Log summary before rendering
	totalNeighbors := 0
	for _, result := range topology.results {
		totalNeighbors += len(result.Neighbors)
	}
	slog.Info("discovery complete",
		"hosts_ok", len(topology.results),
		"hosts_err", len(topology.errors),
		"total_neighbors", totalNeighbors,
	)

	// Render output
	return outputTopology(topology)
}

// topology holds discovery results indexed by source host
type topology struct {
	orderedHosts []string
	results      map[string]*lldp.ParseResult
	errors       map[string]error
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
		conn, err := ssh.CreateConnection(ctx, host)
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

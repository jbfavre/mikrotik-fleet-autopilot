package discover

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"time"

	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
	"jb.favre/mikrotik-fleet-autopilot/common/mndp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// Config controls discovery behavior.
type Config struct {
	UseMNDP     bool
	Interface   string
	MNDPTimeout time.Duration
}

// Dependencies bundles runtime functions and enables tests to inject fakes.
type Dependencies struct {
	CreateSSHConnection func(context.Context, string) (ssh.RunnerInterface, error)
	ListenMNDP          func(context.Context, string, time.Duration) ([]*mndp.Device, error)
}

// Topology holds discovery results indexed by source host.
type Topology struct {
	OrderedHosts   []string
	Results        map[string]*lldp.ParseResult
	Errors         map[string]error
	MNDPByIdentity map[string]*mndp.Device // Identity -> Device
	LLDPPromoted   map[string]struct{}
}

// ReachableHosts returns discovered hosts that were successfully queried via SSH.
func (t *Topology) ReachableHosts() []string {
	if t == nil {
		return nil
	}

	hosts := make([]string, 0, len(t.OrderedHosts))
	for _, host := range t.OrderedHosts {
		if _, ok := t.Results[host]; !ok {
			continue
		}
		hosts = append(hosts, host)
	}
	return hosts
}

// Build discovers topology from static seeds and optional MNDP dynamic seeding.
func Build(ctx context.Context, seedHosts []string, cfg Config, deps Dependencies) (*Topology, error) {
	deps = withDefaults(deps)

	hostSeen := make(map[string]struct{}, len(seedHosts))
	hosts := make([]string, 0, len(seedHosts))
	for _, host := range seedHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if _, seen := hostSeen[host]; seen {
			continue
		}
		hostSeen[host] = struct{}{}
		hosts = append(hosts, host)
	}

	var mndpByIdentity map[string]*mndp.Device
	if cfg.UseMNDP {
		slog.Info("starting MNDP discovery", "interface", cfg.Interface, "timeout", cfg.MNDPTimeout)
		devices, err := deps.ListenMNDP(ctx, cfg.Interface, cfg.MNDPTimeout)
		if err != nil {
			return nil, fmt.Errorf("mndp discovery failed: %w", err)
		}

		mndpByIdentity = make(map[string]*mndp.Device, len(devices))
		for _, d := range devices {
			identity := strings.TrimSpace(d.Identity)
			if identity == "" {
				slog.Warn("MNDP device with empty identity skipped", "mac", d.MACAddress)
				continue
			}
			mndpByIdentity[identity] = d
			if _, seen := hostSeen[identity]; seen {
				continue
			}
			hostSeen[identity] = struct{}{}
			hosts = append(hosts, identity)
		}
		slog.Info("MNDP discovery complete", "devices", len(devices), "addressable", len(hosts))
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts configured for discovery")
	}

	slog.Info("starting LLDP discovery", "hosts", len(hosts))
	topo, err := discoverTopology(ctx, hosts, deps)
	if err != nil {
		return nil, err
	}
	topo.MNDPByIdentity = mndpByIdentity

	if err := expandTopologyFromLLDP(ctx, topo, deps); err != nil {
		return nil, fmt.Errorf("lldp expansion failed: %w", err)
	}

	totalNeighbors := 0
	for _, result := range topo.Results {
		totalNeighbors += len(result.Neighbors)
	}
	slog.Info("discovery complete",
		"hosts_ok", len(topo.Results),
		"hosts_err", len(topo.Errors),
		"total_neighbors", totalNeighbors,
	)

	return topo, nil
}

func withDefaults(deps Dependencies) Dependencies {
	if deps.CreateSSHConnection == nil {
		deps.CreateSSHConnection = ssh.CreateConnection
	}
	if deps.ListenMNDP == nil {
		deps.ListenMNDP = mndp.Listen
	}
	return deps
}

func discoverTopology(ctx context.Context, hosts []string, deps Dependencies) (*Topology, error) {
	topo := &Topology{
		OrderedHosts: make([]string, 0, len(hosts)),
		Results:      make(map[string]*lldp.ParseResult),
		Errors:       make(map[string]error),
	}

	for _, host := range hosts {
		topo.OrderedHosts = append(topo.OrderedHosts, host)
		slog.Info("connecting to host", "host", host)

		conn, err := deps.CreateSSHConnection(ctx, host)
		if err != nil {
			slog.Warn("connection failed", "host", host, "error", err)
			topo.Errors[host] = fmt.Errorf("connection failed: %w", err)
			continue
		}
		slog.Info("connected, running LLDP query", "host", host)

		output, err := conn.Run("/ip/neighbor/print detail where discovered-by~\"lldp\"")
		if err != nil {
			slog.Warn("command failed", "host", host, "error", err)
			topo.Errors[host] = fmt.Errorf("command failed: %w", err)
			_ = conn.Close()
			continue
		}

		result, err := lldp.ParseNeighbors(output)
		if err != nil {
			slog.Warn("parse failed", "host", host, "error", err)
			topo.Errors[host] = fmt.Errorf("parse failed: %w", err)
			_ = conn.Close()
			continue
		}

		if len(result.Warnings) > 0 {
			for _, warn := range result.Warnings {
				slog.Debug("parse warning", "host", host, "warning", warn)
			}
		}

		topo.Results[host] = result
		slog.Info("discovered neighbors", "host", host, "count", len(result.Neighbors))
		_ = conn.Close()
	}

	return topo, nil
}

func expandTopologyFromLLDP(ctx context.Context, topo *Topology, deps Dependencies) error {
	if topo == nil {
		return nil
	}
	if topo.LLDPPromoted == nil {
		topo.LLDPPromoted = make(map[string]struct{})
	}

	seen := make(map[string]struct{}, len(topo.OrderedHosts))
	for _, host := range topo.OrderedHosts {
		seen[host] = struct{}{}
	}

	for {
		candidates := collectLLDPTargets(topo, seen)
		if len(candidates) == 0 {
			return nil
		}

		hosts := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			hosts = append(hosts, candidate.target)
		}

		nextTopo, err := discoverTopology(ctx, hosts, deps)
		if err != nil {
			return err
		}

		added := false
		for _, host := range nextTopo.OrderedHosts {
			if _, ok := nextTopo.Results[host]; !ok {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
			topo.OrderedHosts = append(topo.OrderedHosts, host)
			topo.LLDPPromoted[host] = struct{}{}
			added = true
		}

		maps.Copy(topo.Results, nextTopo.Results)
		maps.Copy(topo.Errors, nextTopo.Errors)

		if !added {
			return nil
		}
	}
}

type lldpDiscoveryCandidate struct {
	target string
	order  int
}

func collectLLDPTargets(topo *Topology, seen map[string]struct{}) []lldpDiscoveryCandidate {
	if topo == nil {
		return nil
	}

	candidates := make([]lldpDiscoveryCandidate, 0)
	seenTargets := make(map[string]struct{})
	order := 0

	for _, sourceHost := range topo.OrderedHosts {
		result := topo.Results[sourceHost]
		if result == nil {
			continue
		}
		for _, neighbor := range result.Neighbors {
			target := resolveLLDPTarget(neighbor)
			if target == "" {
				continue
			}
			if _, ok := seen[target]; ok {
				continue
			}
			if _, ok := seenTargets[target]; ok {
				continue
			}
			seenTargets[target] = struct{}{}
			candidates = append(candidates, lldpDiscoveryCandidate{target: target, order: order})
			order++
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].order < candidates[j].order
	})

	return candidates
}

func resolveLLDPTarget(neighbor *lldp.Neighbor) string {
	if neighbor == nil {
		return ""
	}
	return strings.TrimSpace(neighbor.Identity)
}

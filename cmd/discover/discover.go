package discover

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"

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

// lookupIPv4ByIdentity resolves identity to canonical IPv4 addresses; injectable for testing.
var lookupIPv4ByIdentity = func(ctx context.Context, identity string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip4", identity)
}

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
		canonicalSeen := make(map[string]struct{}, len(devices))
		for _, d := range devices {
			lookupIdentity := d.BaseIdentity
			if lookupIdentity == "" {
				lookupIdentity = d.Identity
			}

			ips, lookupErr := lookupIPv4ByIdentity(ctx, lookupIdentity)
			canonicalIPv4 := firstIPv4String(ips)
			if lookupErr != nil || canonicalIPv4 == "" {
				if d.IPv4Address != "" {
					if lookupErr != nil {
						slog.Warn("MNDP identity DNS lookup failed; falling back to MNDP-reported IPv4", "identity", d.Identity, "base_identity", lookupIdentity, "fallback_ipv4", d.IPv4Address, "error", lookupErr)
					} else {
						slog.Warn("MNDP identity resolved without IPv4 addresses; falling back to MNDP-reported IPv4", "identity", d.Identity, "base_identity", lookupIdentity, "fallback_ipv4", d.IPv4Address)
					}
					canonicalIPv4 = d.IPv4Address
				} else {
					if lookupErr != nil {
						slog.Warn("MNDP identity has no canonical DNS IPv4 and no MNDP IPv4 fallback", "identity", d.Identity, "base_identity", lookupIdentity, "error", lookupErr)
					} else {
						slog.Warn("MNDP identity resolved without IPv4 addresses and no MNDP IPv4 fallback", "identity", d.Identity, "base_identity", lookupIdentity)
					}
					continue
				}
			}

			d.CanonicalIPv4 = canonicalIPv4
			if _, seen := canonicalSeen[canonicalIPv4]; seen {
				slog.Warn("duplicate canonical IPv4 from MNDP identities; skipping duplicate addressable host", "identity", d.Identity, "canonical_ipv4", canonicalIPv4)
				continue
			}
			canonicalSeen[canonicalIPv4] = struct{}{}

			mndpByIP[canonicalIPv4] = d
			ipToIdentity[canonicalIPv4] = d.Identity
			hosts = append(hosts, canonicalIPv4)
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

	if err := expandTopologyFromLLDP(ctx, topo); err != nil {
		return fmt.Errorf("lldp expansion failed: %w", err)
	}

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

func firstIPv4String(ips []net.IP) string {
	for _, ip := range ips {
		ipv4 := ip.To4()
		if ipv4 == nil {
			continue
		}
		return ipv4.String()
	}
	return ""
}

// topology holds discovery results indexed by source host
type topology struct {
	orderedHosts []string
	results      map[string]*lldp.ParseResult
	errors       map[string]error
	mndpByIP     map[string]*mndp.Device // IPv4Address → Device
	ipToIdentity map[string]string       // IPv4Address → MNDP Identity
	lldpPromoted map[string]struct{}
}

func expandTopologyFromLLDP(ctx context.Context, topo *topology) error {
	if topo == nil {
		return nil
	}
	if topo.lldpPromoted == nil {
		topo.lldpPromoted = make(map[string]struct{})
	}

	seen := make(map[string]struct{}, len(topo.orderedHosts))
	for _, host := range topo.orderedHosts {
		seen[host] = struct{}{}
	}

	for {
		candidates := collectLLDPTargets(ctx, topo, seen)
		if len(candidates) == 0 {
			return nil
		}

		for _, candidate := range candidates {
			if candidate.identity != "" {
				if topo.ipToIdentity == nil {
					topo.ipToIdentity = make(map[string]string)
				}
				if _, ok := topo.ipToIdentity[candidate.target]; !ok {
					topo.ipToIdentity[candidate.target] = candidate.identity
				}
			}
		}

		hosts := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			hosts = append(hosts, candidate.target)
		}

		nextTopo, err := discoverTopology(ctx, hosts)
		if err != nil {
			return err
		}

		added := false
		for _, host := range nextTopo.orderedHosts {
			if _, ok := nextTopo.results[host]; !ok {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
			topo.orderedHosts = append(topo.orderedHosts, host)
			topo.lldpPromoted[host] = struct{}{}
			added = true
		}

		for host, result := range nextTopo.results {
			topo.results[host] = result
		}
		for host, err := range nextTopo.errors {
			topo.errors[host] = err
		}

		if !added {
			return nil
		}
	}
}

type lldpDiscoveryCandidate struct {
	target   string
	identity string
	order    int
}

func collectLLDPTargets(ctx context.Context, topo *topology, seen map[string]struct{}) []lldpDiscoveryCandidate {
	if topo == nil {
		return nil
	}

	candidates := make([]lldpDiscoveryCandidate, 0)
	seenTargets := make(map[string]struct{})
	order := 0

	for _, sourceHost := range topo.orderedHosts {
		result := topo.results[sourceHost]
		if result == nil {
			continue
		}
		for _, neighbor := range result.Neighbors {
			target := resolveLLDPTarget(ctx, neighbor)
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
			candidates = append(candidates, lldpDiscoveryCandidate{target: target, identity: strings.TrimSpace(neighbor.Identity), order: order})
			order++
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].order < candidates[j].order
	})

	return candidates
}

func resolveLLDPTarget(ctx context.Context, neighbor *lldp.Neighbor) string {
	if neighbor == nil {
		return ""
	}

	if identity := strings.TrimSpace(neighbor.Identity); identity != "" {
		if ips, err := lookupIPv4ByIdentity(ctx, identity); err == nil {
			if ip := firstIPv4String(ips); ip != "" {
				return ip
			}
		}
	}

	if ip := usableIP(neighbor.Address); ip != "" {
		return ip
	}
	if ip := usableIP(neighbor.Address6); ip != "" {
		return ip
	}

	return ""
}

func usableIP(value string) string {
	parsed := net.ParseIP(strings.TrimSpace(value))
	if parsed == nil {
		return ""
	}
	if parsed.To4() != nil {
		return parsed.String()
	}
	if parsed.IsLinkLocalUnicast() {
		return ""
	}
	return parsed.String()
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

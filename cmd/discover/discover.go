package discover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
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
	var mndpByIdentity map[string]*mndp.Device
	var identityToIP map[string]string

	if cfg.UseMNDP {
		slog.Info("starting MNDP discovery", "interface", cfg.Interface, "timeout", cfg.MNDPTimeout)
		devices, err := listenMNDP(ctx, cfg.Interface, cfg.MNDPTimeout)
		if err != nil {
			if errors.Is(err, mndp.ErrDuplicateIdentity) {
				return err
			}
			slog.Warn("MNDP discovery failed", "error", err)
			// non-fatal: continue with empty host list (will error below)
		}
		mndpByIdentity = make(map[string]*mndp.Device, len(devices))
		identityToIP = make(map[string]string, len(devices))
		identitySeen := make(map[string]struct{}, len(devices))
		for _, d := range devices {
			identity := strings.TrimSpace(d.Identity)
			if identity == "" {
				slog.Warn("MNDP device has no identity; skipping device", "mac", d.MACAddress)
				continue
			}
			if _, seen := identitySeen[identity]; seen {
				return fmt.Errorf("duplicate device identity %q discovered — identities must be unique across the fleet", identity)
			}

			lookupIdentity := d.BaseIdentity
			if lookupIdentity == "" {
				lookupIdentity = identity
			}

			ips, lookupErr := lookupIPv4ByIdentity(ctx, lookupIdentity)
			canonicalIPv4 := firstIPv4String(ips)
			if lookupErr != nil || canonicalIPv4 == "" {
				slog.Warn("MNDP identity lookup failed; skipping device", "identity", identity, "base_identity", lookupIdentity, "dns_error", lookupErr)
				continue
			}

			d.CanonicalIPv4 = canonicalIPv4
			identitySeen[identity] = struct{}{}
			mndpByIdentity[identity] = d
			identityToIP[identity] = canonicalIPv4
			hosts = append(hosts, identity)
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
	topo.mndpByIdentity = mndpByIdentity
	topo.identityToIP = identityToIP

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
	orderedHosts   []string
	results        map[string]*lldp.ParseResult
	errors         map[string]error
	mndpByIdentity map[string]*mndp.Device // Identity → Device
	identityToIP   map[string]string       // Identity → IPv4 (display metadata only)
	lldpPromoted   map[string]struct{}
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
		candidates, err := collectLLDPTargets(ctx, topo, seen)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		for _, candidate := range candidates {
			if candidate.ip == "" {
				continue
			}
			if topo.identityToIP == nil {
				topo.identityToIP = make(map[string]string)
			}
			if _, ok := topo.identityToIP[candidate.target]; !ok {
				topo.identityToIP[candidate.target] = candidate.ip
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

		maps.Copy(topo.results, nextTopo.results)
		maps.Copy(topo.errors, nextTopo.errors)

		if !added {
			return nil
		}
	}
}

type lldpDiscoveryCandidate struct {
	target     string
	ip         string
	order      int
	sourceHost string
	address    string
	address6   string
	mac        string
}

func collectLLDPTargets(ctx context.Context, topo *topology, seen map[string]struct{}) ([]lldpDiscoveryCandidate, error) {
	if topo == nil {
		return nil, nil
	}

	candidates := make([]lldpDiscoveryCandidate, 0)
	seenTargets := make(map[string]lldpDiscoveryCandidate)
	order := 0

	for _, sourceHost := range topo.orderedHosts {
		result := topo.results[sourceHost]
		if result == nil {
			continue
		}
		for _, neighbor := range result.Neighbors {
			target, resolvedIP := resolveLLDPTarget(ctx, neighbor)
			if target == "" {
				continue
			}
			if _, ok := seen[target]; ok {
				continue
			}
			candidate := lldpDiscoveryCandidate{
				target:     target,
				ip:         resolvedIP,
				order:      order,
				sourceHost: sourceHost,
				address:    strings.TrimSpace(neighbor.Address),
				address6:   strings.TrimSpace(neighbor.Address6),
				mac:        strings.TrimSpace(neighbor.MacAddress),
			}
			if existing, ok := seenTargets[target]; ok {
				if conflictingLLDPTarget(existing, candidate) {
					return nil, fmt.Errorf("duplicate device identity %q discovered via LLDP with conflicting metadata (%s vs %s) — identities must be unique across the fleet", target, describeLLDPTarget(existing), describeLLDPTarget(candidate))
				}
				continue
			}
			seenTargets[target] = candidate
			candidates = append(candidates, candidate)
			order++
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].order < candidates[j].order
	})

	return candidates, nil
}

func conflictingLLDPTarget(first lldpDiscoveryCandidate, second lldpDiscoveryCandidate) bool {
	return conflictingNonEmptyValue(first.address, second.address) ||
		conflictingNonEmptyValue(first.address6, second.address6) ||
		conflictingNonEmptyValue(first.mac, second.mac)
}

func conflictingNonEmptyValue(first string, second string) bool {
	return first != "" && second != "" && first != second
}

func describeLLDPTarget(candidate lldpDiscoveryCandidate) string {
	parts := []string{fmt.Sprintf("source=%s", candidate.sourceHost)}
	if candidate.address != "" {
		parts = append(parts, fmt.Sprintf("address=%s", candidate.address))
	}
	if candidate.address6 != "" {
		parts = append(parts, fmt.Sprintf("address6=%s", candidate.address6))
	}
	if candidate.mac != "" {
		parts = append(parts, fmt.Sprintf("mac=%s", candidate.mac))
	}
	if candidate.ip != "" {
		parts = append(parts, fmt.Sprintf("dns-ip=%s", candidate.ip))
	}
	return strings.Join(parts, " ")
}

func resolveLLDPTarget(ctx context.Context, neighbor *lldp.Neighbor) (string, string) {
	if neighbor == nil {
		return "", ""
	}

	if identity := strings.TrimSpace(neighbor.Identity); identity != "" {
		if ips, err := lookupIPv4ByIdentity(ctx, identity); err == nil {
			if ip := firstIPv4String(ips); ip != "" {
				return identity, ip
			}
		}
	}

	return "", ""
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

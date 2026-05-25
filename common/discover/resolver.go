package discover

import (
	"context"
	"fmt"
	"log/slog"

	"jb.favre/mikrotik-fleet-autopilot/common/core"
)

// ResolveHostsDiscoveryFirst returns hosts discovered via topology when possible,
// with local router*.rsc fallback as a safety net.
func ResolveHostsDiscoveryFirst(ctx context.Context, cfg Config, deps Dependencies) ([]string, error) {
	topo, err := Build(ctx, nil, cfg, deps)
	if err == nil {
		hosts := topo.ReachableHosts()
		if len(hosts) > 0 {
			return hosts, nil
		}
		slog.Warn("topology discovery returned no reachable hosts; falling back to local router files")
	} else {
		slog.Warn("topology discovery failed; falling back to local router files", "error", err)
	}

	hosts, localErr := core.DiscoverHosts()
	if localErr != nil {
		if err != nil {
			return nil, fmt.Errorf("topology discovery failed (%v) and local router discovery failed: %w", err, localErr)
		}
		return nil, fmt.Errorf("topology discovery returned no reachable hosts and local router discovery failed: %w", localErr)
	}
	return hosts, nil
}

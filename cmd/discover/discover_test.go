package discover

import (
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
)

func TestBuildTopologyGraph_Basic(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"device1": {
			Neighbors: []*lldp.Neighbor{
				{
					LocalInterface:  "sfp1",
					Identity:        "device2",
					RemoteInterface: "sfp2",
					DiscoveredBy:    []string{"lldp"},
				},
			},
		},
		"device2": {
			Neighbors: []*lldp.Neighbor{},
		},
	}

	graph := buildTopologyGraph(results, []string{"device1", "device2"})
	if len(graph.nodes) != 2 {
		t.Fatalf("expected 2 devices in graph, got %d", len(graph.nodes))
	}

	edges := graph.outgoing["device1"]["device2"]
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge from device1 to device2, got %d", len(edges))
	}
	if edges[0].localInterface != "sfp1" {
		t.Errorf("expected local interface sfp1, got %s", edges[0].localInterface)
	}
}

func TestBuildTopologyGraph_RedundantLinksMultiplicity(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"source": {
			Neighbors: []*lldp.Neighbor{
				{
					LocalInterface:  "ether9",
					Identity:        "peer",
					RemoteInterface: "eth1",
				},
				{
					LocalInterface:  "ether10",
					Identity:        "peer",
					RemoteInterface: "eth2",
				},
			},
		},
	}

	graph := buildTopologyGraph(results, []string{"source"})
	edges := graph.outgoing["source"]["peer"]
	if len(edges) != 2 {
		t.Fatalf("expected 2 parallel links source->peer, got %d", len(edges))
	}
}

func TestSelectRoots_PrefersHigherDegree(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"a": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "b"},
				{Identity: "c"},
			},
		},
		"b": {Neighbors: []*lldp.Neighbor{}},
		"c": {Neighbors: []*lldp.Neighbor{}},
	}

	graph := buildTopologyGraph(results, []string{"a", "b", "c"})
	components := connectedComponents(graph)
	roots := selectRoots(graph, components, []string{"a", "b", "c"})
	if len(roots) != 1 {
		t.Fatalf("expected 1 component root, got %d", len(roots))
	}
	if roots[0] != "a" {
		t.Errorf("expected root a (highest degree), got %s", roots[0])
	}
}

func TestShortName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"device.example.com", "device"},
		{"simple", "simple"},
		{"", ""},
	}

	for _, c := range cases {
		if got := shortName(c.in); got != c.want {
			t.Errorf("shortName(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

package discover

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

type stubRunner struct {
	runOutput string
}

func (s *stubRunner) Run(cmd string) (string, error) {
	return s.runOutput, nil
}

func (s *stubRunner) Close() error {
	return nil
}

func (s *stubRunner) IsAlreadyClosedError(err error) bool {
	return false
}

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

	graph, err := buildTopologyGraph(results, []string{"device1", "device2"}, "")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}
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

	graph, err := buildTopologyGraph(results, []string{"source"}, "")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}
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

	graph, err := buildTopologyGraph(results, []string{"a", "b", "c"}, "")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}
	components := connectedComponents(graph)
	roots := selectRoots(graph, components, []string{"a", "b", "c"}, "")
	if len(roots) != 1 {
		t.Fatalf("expected 1 component root, got %d", len(roots))
	}
	if roots[0] != "a" {
		t.Errorf("expected root a (highest degree), got %s", roots[0])
	}
}

func TestBuildTopologyGraph_ConnectedToAddsMFANode(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"router1": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "router2"},
			},
		},
		"router2": {Neighbors: []*lldp.Neighbor{}},
	}

	graph, err := buildTopologyGraph(results, []string{"router1", "router2"}, "router2")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}

	if _, ok := graph.nodes[mfaNodeName]; !ok {
		t.Fatalf("expected synthetic %q node to exist", mfaNodeName)
	}
	if graph.nodes[mfaNodeName].isSource {
		t.Fatalf("expected synthetic %q node to be non-source", mfaNodeName)
	}
	edges := graph.outgoing[mfaNodeName]["router2"]
	if len(edges) != 1 {
		t.Fatalf("expected 1 synthetic edge from %s to router2, got %d", mfaNodeName, len(edges))
	}

	components := connectedComponents(graph)
	roots := selectRoots(graph, components, []string{"router1", "router2"}, preferredRoot(graph))
	if len(roots) != 1 {
		t.Fatalf("expected 1 component root, got %d", len(roots))
	}
	if roots[0] != mfaNodeName {
		t.Fatalf("expected synthetic node %q to be selected as root, got %q", mfaNodeName, roots[0])
	}
}

func TestBuildTopologyGraph_ConnectedToUnknownTargetFails(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"router1": {Neighbors: []*lldp.Neighbor{}},
	}

	_, err := buildTopologyGraph(results, []string{"router1"}, "router2")
	if err == nil {
		t.Fatal("expected error for unknown connected-to target, got nil")
	}
	if !strings.Contains(err.Error(), "router2") {
		t.Fatalf("expected error to mention missing target, got %v", err)
	}
}

func TestRunDiscoverForHosts_DoesNotConnectToSyntheticMFANode(t *testing.T) {
	ctx := context.WithValue(context.Background(), core.ConfigKey, &core.Config{
		Hosts: []string{"router1"},
	})

	var connectedHosts []string
	originalFactory := createSSHConnection
	createSSHConnection = func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
		connectedHosts = append(connectedHosts, host)
		return &stubRunner{runOutput: ""}, nil
	}
	defer func() {
		createSSHConnection = originalFactory
	}()

	if err := runDiscoverForHosts(ctx, io.Discard, "router1"); err != nil {
		t.Fatalf("runDiscoverForHosts() unexpected error = %v", err)
	}

	if !reflect.DeepEqual(connectedHosts, []string{"router1"}) {
		t.Fatalf("expected SSH connections only for configured hosts, got %v", connectedHosts)
	}
	for _, host := range connectedHosts {
		if host == mfaNodeName {
			t.Fatalf("synthetic node %q must never be used as an SSH target", mfaNodeName)
		}
	}
}

func TestOutputTopology_ConnectedToRendersMFAAsRoot(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2"},
				},
			},
			"router2": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, "router2"); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	if !strings.Contains(out.String(), "[mfa]") {
		t.Fatalf("expected rendered topology to contain mfa root, got output:\n%s", out.String())
	}
	lines := strings.Split(out.String(), "\n")
	rootLineFound := false
	for _, line := range lines {
		if strings.HasPrefix(line, "[") {
			rootLineFound = true
			if line != "[mfa]" {
				t.Fatalf("expected first rendered root to be [mfa], got %q", line)
			}
			break
		}
	}
	if !rootLineFound {
		t.Fatalf("expected rendered topology to contain a root line, got output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "via ? ↔ ?") {
		t.Fatalf("expected synthetic mfa edge to avoid empty via lines, got output:\n%s", out.String())
	}
}

func TestOutputTopology_RendersViaLineForSingleLink(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2", LocalInterface: "ether1", RemoteInterface: "sfp1"},
				},
			},
			"router2": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	if !strings.Contains(out.String(), "via ether1 ↔ sfp1") {
		t.Fatalf("expected output to contain via line for single link, got output:\n%s", out.String())
	}
}

func TestOutputTopology_RendersViaLinesForParallelLinks(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2", LocalInterface: "ether1", RemoteInterface: "sfp1"},
					{Identity: "router2", LocalInterface: "ether2", RemoteInterface: "sfp2"},
				},
			},
			"router2": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	if !strings.Contains(out.String(), "via ether1 ↔ sfp1") {
		t.Fatalf("expected output to contain first via line for parallel links, got output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "via ether2 ↔ sfp2") {
		t.Fatalf("expected output to contain second via line for parallel links, got output:\n%s", out.String())
	}
}

func TestOutputTopology_RendersViaFallbackForMissingLocalInterface(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2", RemoteInterface: "sfp1"},
				},
			},
			"router2": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	if !strings.Contains(out.String(), "via ? ↔ sfp1") {
		t.Fatalf("expected output to contain fallback via line for missing local interface, got output:\n%s", out.String())
	}
}

func TestOutputTopology_ViaLineShowsVerticalBarWhenChildrenExist(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2", "router3", "router4"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2", LocalInterface: "ether1", RemoteInterface: "sfp1"},
				},
			},
			"router2": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router3", LocalInterface: "ether2", RemoteInterface: "sfp2"},
				},
			},
			"router3": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router4", LocalInterface: "ether3", RemoteInterface: "sfp3"},
				},
			},
			"router4": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	lines := strings.Split(output, "\n")
	foundViaWithBar := false
	for _, line := range lines {
		if strings.Contains(line, "│  via") {
			foundViaWithBar = true
			break
		}
	}
	if !foundViaWithBar {
		t.Fatalf("expected via line with vertical bar (│) when child nodes exist, got output:\n%s", output)
	}
}

func TestOutputTopology_ViaLineHasNoBarForLeafNode(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2", LocalInterface: "ether1", RemoteInterface: "sfp1"},
				},
			},
			"router2": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "via ether1 ↔ sfp1") {
			if strings.HasPrefix(strings.TrimLeft(line, " "), "│") {
				t.Fatalf("expected via line WITHOUT vertical bar (|) for leaf node, but found bar in: %q", line)
			}
			return
		}
	}
	t.Fatalf("expected to find via line in output, got output:\n%s", output)
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

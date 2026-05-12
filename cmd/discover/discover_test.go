package discover

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
	"jb.favre/mikrotik-fleet-autopilot/common/mndp"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

type stubRunner struct {
	runOutput string
}

func (s *stubRunner) Run(cmd string) (string, error) {
	return s.runOutput, nil
}

func (s *stubRunner) RunInteractive(input string) (string, error) {
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

	topo := &topology{
		orderedHosts: []string{"device1", "device2"},
		results:      results,
		errors:       map[string]error{},
	}
	graph, err := buildTopologyGraph(topo, "")
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

	topo := &topology{
		orderedHosts: []string{"source"},
		results:      results,
		errors:       map[string]error{},
	}
	graph, err := buildTopologyGraph(topo, "")
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

	topo := &topology{
		orderedHosts: []string{"a", "b", "c"},
		results:      results,
		errors:       map[string]error{},
	}
	graph, err := buildTopologyGraph(topo, "")
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

	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results:      results,
		errors:       map[string]error{},
	}
	graph, err := buildTopologyGraph(topo, "router2")
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

	topo := &topology{
		orderedHosts: []string{"router1"},
		results:      results,
		errors:       map[string]error{},
	}
	_, err := buildTopologyGraph(topo, "router2")
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
	lines := strings.SplitSeq(output, "\n")
	for line := range lines {
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

func TestOutputTopology_RendersUpgradePlanSection(t *testing.T) {
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
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Upgrade Plan") {
		t.Fatalf("expected output to contain Upgrade Plan section, got:\n%s", output)
	}
	if !strings.Contains(output, "Wave") {
		t.Fatalf("expected output to contain wave lines, got:\n%s", output)
	}
}

func TestOutputTopology_UpgradePlanExcludesNonSourceNodes(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "external-switch"},
				},
			},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Excluded") {
		t.Fatalf("expected output to mention excluded non-upgradeable devices, got:\n%s", output)
	}
	if !strings.Contains(output, "external-switch") {
		t.Fatalf("expected excluded list to contain external-switch, got:\n%s", output)
	}
}

func TestOutputTopology_UpgradeSummaryMetrics(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2", "router3"},
		results: map[string]*lldp.ParseResult{
			"router1": {
				Neighbors: []*lldp.Neighbor{
					{Identity: "router2"},
					{Identity: "router3"},
				},
			},
			"router2": {Neighbors: []*lldp.Neighbor{}},
			"router3": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Upgradeable devices") {
		t.Fatalf("expected summary to contain Upgradeable devices metric, got:\n%s", output)
	}
	if !strings.Contains(output, "Upgrade waves") {
		t.Fatalf("expected summary to contain Upgrade waves metric, got:\n%s", output)
	}
	if !strings.Contains(output, "Max wave parallelism") {
		t.Fatalf("expected summary to contain Max wave parallelism metric, got:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// MNDP path tests
// ---------------------------------------------------------------------------

func TestRunDiscoverForHosts_MNDPSuccessfulDiscovery(t *testing.T) {
	// Inject a mock MNDP listener that returns one device with an IPv4 address
	originalListen := listenMNDP
	listenMNDP = func(_ context.Context, _ string, _ time.Duration) ([]*mndp.Device, error) {
		return []*mndp.Device{
			{MACAddress: "aa:bb:cc:dd:ee:ff", Identity: "router.home", IPv4Address: "192.168.1.1"},
		}, nil
	}
	defer func() { listenMNDP = originalListen }()
	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, identity string) ([]net.IP, error) {
		if identity != "router.home" {
			t.Fatalf("unexpected DNS lookup identity %q", identity)
		}
		return []net.IP{net.ParseIP("192.168.1.1")}, nil
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	// SSH stub returns empty LLDP output
	originalSSH := createSSHConnection
	createSSHConnection = func(_ context.Context, _ string) (ssh.RunnerInterface, error) {
		return &stubRunner{runOutput: ""}, nil
	}
	defer func() { createSSHConnection = originalSSH }()

	cfg := &core.Config{UseMNDP: true, MNDPTimeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)

	var out bytes.Buffer
	if err := runDiscoverForHosts(ctx, &out, ""); err != nil {
		t.Fatalf("runDiscoverForHosts() unexpected error = %v", err)
	}
	// The topology should display "router.home" (MNDP identity), not the bare IP
	if !strings.Contains(out.String(), "router.home") {
		t.Errorf("expected output to contain MNDP identity 'router.home', got:\n%s", out.String())
	}
}

func TestRunDiscoverForHosts_MNDPSSHFails(t *testing.T) {
	// MNDP returns a device
	originalListen := listenMNDP
	listenMNDP = func(_ context.Context, _ string, _ time.Duration) ([]*mndp.Device, error) {
		return []*mndp.Device{
			{MACAddress: "aa:bb:cc:dd:ee:ff", Identity: "router.home", IPv4Address: "192.168.1.1"},
		}, nil
	}
	defer func() { listenMNDP = originalListen }()
	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.168.1.1")}, nil
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	// SSH connection fails
	originalSSH := createSSHConnection
	createSSHConnection = func(_ context.Context, _ string) (ssh.RunnerInterface, error) {
		return nil, errors.New("connection refused")
	}
	defer func() { createSSHConnection = originalSSH }()

	cfg := &core.Config{UseMNDP: true, MNDPTimeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)

	var out bytes.Buffer
	// Should not return an error (SSH failure is recorded, not fatal)
	if err := runDiscoverForHosts(ctx, &out, ""); err != nil {
		t.Fatalf("runDiscoverForHosts() unexpected error = %v", err)
	}
	// The error section should appear in the output
	output := out.String()
	if !strings.Contains(output, "Discovery Errors") {
		t.Errorf("expected output to contain 'Discovery Errors', got:\n%s", output)
	}
}

func TestRunDiscoverForHosts_MNDPUsesFirstCanonicalIPv4(t *testing.T) {
	originalListen := listenMNDP
	listenMNDP = func(_ context.Context, _ string, _ time.Duration) ([]*mndp.Device, error) {
		return []*mndp.Device{{MACAddress: "aa:bb:cc:dd:ee:ff", Identity: "router.home", BaseIdentity: "router.home", IPv4Address: "10.0.0.10"}}, nil
	}
	defer func() { listenMNDP = originalListen }()

	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, identity string) ([]net.IP, error) {
		if identity != "router.home" {
			t.Fatalf("unexpected DNS lookup identity %q", identity)
		}
		return []net.IP{net.ParseIP("192.168.99.10"), net.ParseIP("192.168.99.11")}, nil
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	var connectedHosts []string
	originalSSH := createSSHConnection
	createSSHConnection = func(_ context.Context, host string) (ssh.RunnerInterface, error) {
		connectedHosts = append(connectedHosts, host)
		return &stubRunner{runOutput: ""}, nil
	}
	defer func() { createSSHConnection = originalSSH }()

	cfg := &core.Config{UseMNDP: true, MNDPTimeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)

	if err := runDiscoverForHosts(ctx, io.Discard, ""); err != nil {
		t.Fatalf("runDiscoverForHosts() unexpected error = %v", err)
	}

	if !reflect.DeepEqual(connectedHosts, []string{"192.168.99.10"}) {
		t.Fatalf("expected SSH to use only first canonical IPv4, got %v", connectedHosts)
	}
}

func TestRunDiscoverForHosts_MNDPDNSFailureExcludesDevice(t *testing.T) {
	originalListen := listenMNDP
	listenMNDP = func(_ context.Context, _ string, _ time.Duration) ([]*mndp.Device, error) {
		return []*mndp.Device{{MACAddress: "aa:bb:cc:dd:ee:ff", Identity: "router.home", BaseIdentity: "router.home", IPv4Address: "10.0.0.10"}}, nil
	}
	defer func() { listenMNDP = originalListen }()

	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, _ string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	cfg := &core.Config{UseMNDP: true, MNDPTimeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)

	err := runDiscoverForHosts(ctx, io.Discard, "")
	if err == nil {
		t.Fatal("runDiscoverForHosts() expected no hosts error when canonical DNS lookup fails")
	}
	if !strings.Contains(err.Error(), "no hosts") {
		t.Fatalf("expected no hosts error, got %v", err)
	}
}

func TestRunDiscoverForHosts_MNDPZeroDevices(t *testing.T) {
	// MNDP returns no devices
	originalListen := listenMNDP
	listenMNDP = func(_ context.Context, _ string, _ time.Duration) ([]*mndp.Device, error) {
		return []*mndp.Device{}, nil
	}
	defer func() { listenMNDP = originalListen }()
	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.168.1.1")}, nil
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	cfg := &core.Config{UseMNDP: true, MNDPTimeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)

	err := runDiscoverForHosts(ctx, io.Discard, "")
	if err == nil {
		t.Fatal("runDiscoverForHosts() expected error for zero MNDP devices, got nil")
	}
	if !strings.Contains(err.Error(), "no hosts") {
		t.Errorf("expected 'no hosts' error, got: %v", err)
	}
}

func TestRunDiscoverForHosts_MNDPListenError(t *testing.T) {
	// MNDP listener returns an error
	originalListen := listenMNDP
	listenMNDP = func(_ context.Context, _ string, _ time.Duration) ([]*mndp.Device, error) {
		return nil, errors.New("network unreachable")
	}
	defer func() { listenMNDP = originalListen }()
	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.168.1.1")}, nil
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	cfg := &core.Config{UseMNDP: true, MNDPTimeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)

	err := runDiscoverForHosts(ctx, io.Discard, "")
	// MNDP error is non-fatal (warn + empty host list → "no hosts" error)
	if err == nil {
		t.Fatal("runDiscoverForHosts() expected error after MNDP failure with empty hosts, got nil")
	}
	if !strings.Contains(err.Error(), "no hosts") {
		t.Errorf("expected 'no hosts' error after MNDP failure, got: %v", err)
	}
}

func TestBuildTopologyGraph_MNDPIdentityAliasing(t *testing.T) {
	// When ipToIdentity maps an IP to an identity, the node should use the identity as its name
	results := map[string]*lldp.ParseResult{
		"192.168.1.1": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "switch1"},
			},
		},
	}

	topo := &topology{
		orderedHosts: []string{"192.168.1.1"},
		results:      results,
		errors:       map[string]error{},
		ipToIdentity: map[string]string{"192.168.1.1": "router.home"},
	}
	graph, err := buildTopologyGraph(topo, "")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}

	// The node should be named "router.home", not "192.168.1.1"
	if _, ok := graph.nodes["router.home"]; !ok {
		t.Errorf("expected node 'router.home' (from MNDP identity), got nodes: %v", graph.nodes)
	}
	if _, ok := graph.nodes["192.168.1.1"]; ok {
		t.Errorf("expected bare IP '192.168.1.1' to be replaced by MNDP identity")
	}
}

func TestBuildTopologyGraph_ConnectedToSupportsMNDPSourceIP(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"192.168.1.1"},
		results: map[string]*lldp.ParseResult{
			"192.168.1.1": {Neighbors: []*lldp.Neighbor{}},
		},
		errors:       map[string]error{},
		ipToIdentity: map[string]string{"192.168.1.1": "router.home"},
	}

	graph, err := buildTopologyGraph(topo, "192.168.1.1")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}

	edges := graph.outgoing[mfaNodeName]["router.home"]
	if len(edges) != 1 {
		t.Fatalf("expected synthetic edge from %s to router.home, got %d", mfaNodeName, len(edges))
	}
}

func TestBuildTopologyGraph_MNDPDisambiguatedIdentityLabelsPreserved(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"192.168.1.1", "192.168.1.2"},
		results: map[string]*lldp.ParseResult{
			"192.168.1.1": {Neighbors: []*lldp.Neighbor{}},
		},
		errors: map[string]error{
			"192.168.1.2": errors.New("ssh failed"),
		},
		ipToIdentity: map[string]string{
			"192.168.1.1": "router.home #1",
			"192.168.1.2": "router.home #2",
		},
	}

	graph, err := buildTopologyGraph(topo, "")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}

	name1 := "router.home #1"
	name2 := "router.home #2"
	if _, ok := graph.nodes[name1]; !ok {
		t.Fatalf("expected disambiguated node %q to exist", name1)
	}
	if _, ok := graph.nodes[name2]; !ok {
		t.Fatalf("expected disambiguated node %q to exist", name2)
	}

	if n := graph.nodes[name1]; n.sshReachable == nil || !*n.sshReachable {
		t.Fatalf("expected %q to be marked SSH reachable", name1)
	}
	if n := graph.nodes[name2]; n.sshReachable == nil || *n.sshReachable {
		t.Fatalf("expected %q to be marked SSH unreachable", name2)
	}
}

func TestBuildTopologyGraph_SSHReachabilityMarked(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"router1": {Neighbors: []*lldp.Neighbor{}},
	}
	errs := map[string]error{
		"router2": errors.New("connection refused"),
	}

	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results:      results,
		errors:       errs,
	}
	graph, err := buildTopologyGraph(topo, "")
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}

	// router1 had a result → sshReachable = true
	if n, ok := graph.nodes["router1"]; !ok || n.sshReachable == nil || !*n.sshReachable {
		t.Errorf("expected router1 to be SSH reachable")
	}
	// router2 had an error → sshReachable = false
	if n, ok := graph.nodes["router2"]; !ok || n.sshReachable == nil || *n.sshReachable {
		t.Errorf("expected router2 to be SSH unreachable")
	}
}

func TestOutputTopology_UnreachableDeviceRenderedWithPrefix(t *testing.T) {
	// router2 is in ordered hosts but SSH failed
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {Neighbors: []*lldp.Neighbor{{Identity: "router2"}}},
		},
		errors: map[string]error{
			"router2": errors.New("connection refused"),
		},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	// router2 has sshReachable=false → should appear with ❓ prefix in the tree
	if !strings.Contains(output, "❓") {
		t.Errorf("expected ❓ prefix for SSH-unreachable device, got:\n%s", output)
	}
}

func TestOutputTopology_UnreachableCountInSummary(t *testing.T) {
	topo := &topology{
		orderedHosts: []string{"router1", "router2"},
		results: map[string]*lldp.ParseResult{
			"router1": {Neighbors: []*lldp.Neighbor{{Identity: "router2"}}},
		},
		errors: map[string]error{
			"router2": errors.New("connection refused"),
		},
	}

	var out bytes.Buffer
	if err := outputTopology(&out, topo, ""); err != nil {
		t.Fatalf("outputTopology() unexpected error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Unreachable devices") {
		t.Errorf("expected 'Unreachable devices' in summary, got:\n%s", output)
	}
}

func TestRunDiscoverForHosts_LLDPPromotesNeighborHost(t *testing.T) {
	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, identity string) ([]net.IP, error) {
		if identity == "router2" {
			return []net.IP{net.ParseIP("192.168.1.2")}, nil
		}
		return nil, errors.New("not found")
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	originalSSH := createSSHConnection
	createSSHConnection = func(_ context.Context, host string) (ssh.RunnerInterface, error) {
		switch host {
		case "router1":
			return &stubRunner{runOutput: `0 interface=ether1 address=192.168.1.2 mac-address=aa:bb:cc:dd:ee:ff identity="router2" discovered-by=lldp`}, nil
		case "192.168.1.2":
			return &stubRunner{runOutput: ""}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}
	defer func() { createSSHConnection = originalSSH }()

	ctx := context.WithValue(context.Background(), core.ConfigKey, &core.Config{Hosts: []string{"router1"}})

	var out bytes.Buffer
	if err := runDiscoverForHosts(ctx, &out, ""); err != nil {
		t.Fatalf("runDiscoverForHosts() unexpected error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "router2 [LLDP]") {
		t.Fatalf("expected LLDP-promoted node marker in output, got:\n%s", output)
	}
	if !strings.Contains(output, "LLDP auto-discovered") {
		t.Fatalf("expected LLDP summary section in output, got:\n%s", output)
	}
}

func TestRunDiscoverForHosts_LLDPPromotedHostIsSSHTarget(t *testing.T) {
	originalLookup := lookupIPv4ByIdentity
	lookupIPv4ByIdentity = func(_ context.Context, identity string) ([]net.IP, error) {
		if identity == "router2" {
			return []net.IP{net.ParseIP("192.168.1.2")}, nil
		}
		return nil, errors.New("not found")
	}
	defer func() { lookupIPv4ByIdentity = originalLookup }()

	var connectedHosts []string
	originalSSH := createSSHConnection
	createSSHConnection = func(_ context.Context, host string) (ssh.RunnerInterface, error) {
		connectedHosts = append(connectedHosts, host)
		switch host {
		case "router1":
			return &stubRunner{runOutput: `0 interface=ether1 address=192.168.1.2 mac-address=aa:bb:cc:dd:ee:ff identity="router2" discovered-by=lldp`}, nil
		case "192.168.1.2":
			return &stubRunner{runOutput: ""}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}
	defer func() { createSSHConnection = originalSSH }()

	ctx := context.WithValue(context.Background(), core.ConfigKey, &core.Config{Hosts: []string{"router1"}})

	if err := runDiscoverForHosts(ctx, io.Discard, ""); err != nil {
		t.Fatalf("runDiscoverForHosts() unexpected error = %v", err)
	}

	if !reflect.DeepEqual(connectedHosts, []string{"router1", "192.168.1.2"}) {
		t.Fatalf("expected second-pass SSH target to be promoted LLDP host, got %v", connectedHosts)
	}
}

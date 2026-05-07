package discover

import (
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
)

// buildGraphForPlannerTest is a test helper that constructs a topologyGraph
// from LLDP results and fails the test on any error.
func buildGraphForPlannerTest(t *testing.T, results map[string]*lldp.ParseResult, orderedHosts []string, connectedTo string) *topologyGraph {
	t.Helper()
	graph, err := buildTopologyGraph(results, orderedHosts, connectedTo)
	if err != nil {
		t.Fatalf("buildTopologyGraph() unexpected error = %v", err)
	}
	return graph
}

// devicesInWave returns the set of devices scheduled in any wave.
func devicesInWave(plan *upgradePlan) map[string]bool {
	all := make(map[string]bool)
	for _, wave := range plan.waves {
		for _, d := range wave.devices {
			all[d] = true
		}
	}
	return all
}

// TestBuildUpgradePlan_LinearChain verifies that a 3-node linear chain produces
// two waves: the two leaf nodes first, then the central (highest-degree) node.
func TestBuildUpgradePlan_LinearChain(t *testing.T) {
	// device2 is the selected root (highest degree = 2).
	// Spanning tree: device2 → {device1, device3}
	// Wave 1: device1 and device3 (leaves, not adjacent to each other)
	// Wave 2: device2
	results := map[string]*lldp.ParseResult{
		"device1": {Neighbors: []*lldp.Neighbor{{Identity: "device2"}}},
		"device2": {Neighbors: []*lldp.Neighbor{{Identity: "device3"}}},
		"device3": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"device1", "device2", "device3"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	if len(plan.waves) != 3 {
		t.Fatalf("expected 3 waves, got %d: %+v", len(plan.waves), plan.waves)
	}
	if len(plan.waves[0].devices) != 1 {
		t.Fatalf("expected 1 device in wave 1 (leaf), got %d: %v", len(plan.waves[0].devices), plan.waves[0].devices)
	}
	if len(plan.waves[0].devices) != 1 || plan.waves[0].devices[0] != "device3" {
		t.Fatalf("expected [device3] in wave 1, got %v", plan.waves[0].devices)
	}
	if len(plan.waves[1].devices) != 1 || plan.waves[1].devices[0] != "device2" {
		t.Fatalf("expected [device2] in wave 2 (root), got %v", plan.waves[1].devices)
	}
	if len(plan.waves[2].devices) != 1 || plan.waves[2].devices[0] != "device1" {
		t.Fatalf("expected [device1] in wave 3 (leaf), got %v", plan.waves[2].devices)
	}
	if len(plan.excluded) != 0 {
		t.Fatalf("expected no excluded devices, got %v", plan.excluded)
	}
}

// TestBuildUpgradePlan_Star verifies that all leaves are scheduled in the first
// wave and the hub (root) is scheduled last.
func TestBuildUpgradePlan_Star(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"hub": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "leaf1"},
				{Identity: "leaf2"},
				{Identity: "leaf3"},
			},
		},
		"leaf1": {Neighbors: []*lldp.Neighbor{}},
		"leaf2": {Neighbors: []*lldp.Neighbor{}},
		"leaf3": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"hub", "leaf1", "leaf2", "leaf3"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	if len(plan.waves) != 2 {
		t.Fatalf("expected 2 waves, got %d: %+v", len(plan.waves), plan.waves)
	}
	if len(plan.waves[0].devices) != 3 {
		t.Fatalf("expected 3 leaves in wave 1, got %d: %v", len(plan.waves[0].devices), plan.waves[0].devices)
	}
	if len(plan.waves[1].devices) != 1 || plan.waves[1].devices[0] != "hub" {
		t.Fatalf("expected [hub] in final wave, got %v", plan.waves[1].devices)
	}
}

// TestBuildUpgradePlan_CrossLinkedSiblings verifies that two sibling nodes
// sharing a cross-link are placed in different waves (adjacency safety guard).
func TestBuildUpgradePlan_CrossLinkedSiblings(t *testing.T) {
	// Topology: root → nodeA, root → nodeB, nodeA ↔ nodeB cross-link.
	// All three nodes have degree 2; root wins on sourceOrder (index 0).
	// Spanning tree: root → {nodeA, nodeB}
	// nodeA and nodeB are directly connected, so they cannot share a wave.
	// Expected: wave1=[nodeA], wave2=[nodeB], wave3=[root]
	results := map[string]*lldp.ParseResult{
		"root": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "nodeA"},
				{Identity: "nodeB"},
			},
		},
		"nodeA": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "nodeB"},
			},
		},
		"nodeB": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"root", "nodeA", "nodeB"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	if len(plan.waves) != 3 {
		t.Fatalf("expected 3 waves (adjacency guard splits siblings), got %d: %+v", len(plan.waves), plan.waves)
	}
	if len(plan.waves[0].devices) != 1 {
		t.Fatalf("expected 1 device in wave 1 (adjacency guard), got %v", plan.waves[0].devices)
	}
	if len(plan.waves[1].devices) != 1 {
		t.Fatalf("expected 1 device in wave 2, got %v", plan.waves[1].devices)
	}
	if len(plan.waves[2].devices) != 1 || plan.waves[2].devices[0] != "root" {
		t.Fatalf("expected [root] in final wave, got %v", plan.waves[2].devices)
	}
	// The two sibling nodes must be in different waves.
	wave1Device := plan.waves[0].devices[0]
	wave2Device := plan.waves[1].devices[0]
	if wave1Device == wave2Device {
		t.Fatalf("nodeA and nodeB must not share the same wave")
	}
	if wave1Device != "nodeA" && wave1Device != "nodeB" {
		t.Fatalf("expected nodeA or nodeB in wave 1, got %q", wave1Device)
	}
}

// TestBuildUpgradePlan_NonSourceExclusion verifies that a discovered-only
// (non-source) neighbor is excluded from all waves and listed in plan.excluded.
func TestBuildUpgradePlan_NonSourceExclusion(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"source1": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "discovered-neighbor"},
			},
		},
	}
	orderedHosts := []string{"source1"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	if len(plan.waves) != 1 {
		t.Fatalf("expected 1 wave, got %d: %+v", len(plan.waves), plan.waves)
	}
	if len(plan.waves[0].devices) != 1 || plan.waves[0].devices[0] != "source1" {
		t.Fatalf("expected [source1] in wave 1, got %v", plan.waves[0].devices)
	}
	if len(plan.excluded) != 1 || plan.excluded[0] != "discovered-neighbor" {
		t.Fatalf("expected [discovered-neighbor] in excluded, got %v", plan.excluded)
	}
	scheduledDevices := devicesInWave(plan)
	if scheduledDevices["discovered-neighbor"] {
		t.Fatalf("discovered-neighbor must not appear in any wave")
	}
}

// TestBuildUpgradePlan_IntermediaryNonSourceBetweenSources verifies that an
// upstream source waits for a downstream upgradeable source even when they are
// connected through a discovered-only intermediary.
func TestBuildUpgradePlan_IntermediaryNonSourceBetweenSources(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"source-root": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "intermediate"},
			},
		},
		"source-leaf": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "intermediate"},
			},
		},
	}
	orderedHosts := []string{"source-root", "source-leaf"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	if len(plan.waves) != 2 {
		t.Fatalf("expected 2 waves, got %d: %+v", len(plan.waves), plan.waves)
	}
	if len(plan.waves[0].devices) != 1 || plan.waves[0].devices[0] != "source-leaf" {
		t.Fatalf("expected [source-leaf] in wave 1, got %v", plan.waves[0].devices)
	}
	if len(plan.waves[1].devices) != 1 || plan.waves[1].devices[0] != "source-root" {
		t.Fatalf("expected [source-root] in wave 2, got %v", plan.waves[1].devices)
	}
	if len(plan.excluded) != 1 || plan.excluded[0] != "intermediate" {
		t.Fatalf("expected [intermediate] in excluded, got %v", plan.excluded)
	}

	scheduledDevices := devicesInWave(plan)
	if scheduledDevices["intermediate"] {
		t.Fatalf("intermediate must not appear in any wave")
	}
	if !scheduledDevices["source-root"] || !scheduledDevices["source-leaf"] {
		t.Fatalf("expected both source devices to be scheduled, got %v", scheduledDevices)
	}
}

// TestBuildUpgradePlan_MultipleComponents verifies that waves from disconnected
// components are merged correctly: all leaves in the first wave, all hubs last.
func TestBuildUpgradePlan_MultipleComponents(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"hub1":   {Neighbors: []*lldp.Neighbor{{Identity: "leaf1a"}, {Identity: "leaf1b"}}},
		"leaf1a": {Neighbors: []*lldp.Neighbor{}},
		"leaf1b": {Neighbors: []*lldp.Neighbor{}},
		"hub2":   {Neighbors: []*lldp.Neighbor{{Identity: "leaf2a"}}},
		"leaf2a": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"hub1", "leaf1a", "leaf1b", "hub2", "leaf2a"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	// Expected: wave1=[leaf1a, leaf1b, leaf2a], wave2=[hub1, hub2]
	if len(plan.waves) != 2 {
		t.Fatalf("expected 2 waves, got %d: %+v", len(plan.waves), plan.waves)
	}
	if len(plan.waves[0].devices) != 3 {
		t.Fatalf("expected 3 leaf devices in wave 1, got %d: %v", len(plan.waves[0].devices), plan.waves[0].devices)
	}
	if len(plan.waves[1].devices) != 2 {
		t.Fatalf("expected 2 hub devices in wave 2, got %d: %v", len(plan.waves[1].devices), plan.waves[1].devices)
	}
	// All 5 source nodes must be scheduled exactly once.
	scheduled := devicesInWave(plan)
	for _, host := range orderedHosts {
		if !scheduled[host] {
			t.Fatalf("expected %q to be scheduled in a wave", host)
		}
	}
	if len(plan.excluded) != 0 {
		t.Fatalf("expected no excluded devices, got %v", plan.excluded)
	}
}

// TestBuildUpgradePlan_MFANodeNeverInWaves verifies that the synthetic mfa node
// is never scheduled, even when it is the graph root (connected-to mode).
func TestBuildUpgradePlan_MFANodeNeverInWaves(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"router1": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"router1"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "router1")

	plan := buildUpgradePlan(graph, orderedHosts)

	for _, wave := range plan.waves {
		for _, device := range wave.devices {
			if device == mfaNodeName {
				t.Fatalf("mfa node must never appear in any wave, found in wave %d", wave.index)
			}
		}
	}
	// router1 must still be scheduled.
	if !devicesInWave(plan)["router1"] {
		t.Fatalf("expected router1 to be scheduled in a wave, got waves: %+v", plan.waves)
	}
}

// TestBuildUpgradePlan_WaveIndicesAreSequential verifies that wave index values
// are contiguous starting from 1.
func TestBuildUpgradePlan_WaveIndicesAreSequential(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"r1": {Neighbors: []*lldp.Neighbor{{Identity: "r2"}}},
		"r2": {Neighbors: []*lldp.Neighbor{{Identity: "r3"}}},
		"r3": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"r1", "r2", "r3"}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	for i, wave := range plan.waves {
		if wave.index != i+1 {
			t.Fatalf("expected wave index %d at position %d, got %d", i+1, i, wave.index)
		}
	}
}

// TestBuildUpgradePlan_EmptyGraph verifies that an empty upgradeable set
// produces a plan with no waves and no excluded nodes.
func TestBuildUpgradePlan_EmptyUpgradeable(t *testing.T) {
	// Build a graph where the only source has no results (so no source node is
	// added by the neighbor loop), but we pass an empty orderedHosts.
	results := map[string]*lldp.ParseResult{}
	orderedHosts := []string{}
	graph := buildGraphForPlannerTest(t, results, orderedHosts, "")

	plan := buildUpgradePlan(graph, orderedHosts)

	if len(plan.waves) != 0 {
		t.Fatalf("expected 0 waves for empty graph, got %d", len(plan.waves))
	}
	if len(plan.excluded) != 0 {
		t.Fatalf("expected 0 excluded for empty graph, got %v", plan.excluded)
	}
}

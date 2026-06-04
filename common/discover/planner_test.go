package discover

import (
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
)

func buildPlanForTest(t *testing.T, results map[string]*lldp.ParseResult, orderedHosts []string) *UpgradePlan {
	t.Helper()
	plan, err := BuildUpgradePlan(&Topology{
		OrderedHosts: orderedHosts,
		Results:      results,
		Errors:       map[string]error{},
	})
	if err != nil {
		t.Fatalf("BuildUpgradePlan() unexpected error = %v", err)
	}
	return plan
}

func planDevices(plan *UpgradePlan) map[string]bool {
	all := make(map[string]bool)
	for _, wave := range plan.Waves {
		for _, d := range wave.Devices {
			all[d] = true
		}
	}
	return all
}

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
	plan := buildPlanForTest(t, results, orderedHosts)

	if len(plan.Waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(plan.Waves))
	}
	if len(plan.Waves[0].Devices) != 3 {
		t.Fatalf("expected 3 leaf devices in wave 1, got %d", len(plan.Waves[0].Devices))
	}
	if len(plan.Waves[1].Devices) != 1 || plan.Waves[1].Devices[0] != "hub" {
		t.Fatalf("expected [hub] in final wave, got %v", plan.Waves[1].Devices)
	}
}

func TestBuildUpgradePlan_CrossLinkedSiblings(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"root": {
			Neighbors: []*lldp.Neighbor{
				{Identity: "nodeA"},
				{Identity: "nodeB"},
			},
		},
		"nodeA": {Neighbors: []*lldp.Neighbor{{Identity: "nodeB"}}},
		"nodeB": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"root", "nodeA", "nodeB"}
	plan := buildPlanForTest(t, results, orderedHosts)

	if len(plan.Waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(plan.Waves))
	}
	if plan.Waves[2].Devices[0] != "root" {
		t.Fatalf("expected root in final wave, got %v", plan.Waves[2].Devices)
	}
	if len(plan.Waves[0].Devices) != 1 || len(plan.Waves[1].Devices) != 1 {
		t.Fatalf("expected sibling split across wave 1 and 2, got wave1=%v wave2=%v", plan.Waves[0].Devices, plan.Waves[1].Devices)
	}
}

func TestBuildUpgradePlan_NonSourceExclusion(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"source1": {Neighbors: []*lldp.Neighbor{{Identity: "discovered-neighbor"}}},
	}
	orderedHosts := []string{"source1"}
	plan := buildPlanForTest(t, results, orderedHosts)

	if len(plan.Waves) != 1 || len(plan.Waves[0].Devices) != 1 || plan.Waves[0].Devices[0] != "source1" {
		t.Fatalf("expected only source1 scheduled, got %v", plan.Waves)
	}
	if len(plan.Excluded) != 1 || plan.Excluded[0] != "discovered-neighbor" {
		t.Fatalf("expected discovered neighbor to be excluded, got %v", plan.Excluded)
	}
	if planDevices(plan)["discovered-neighbor"] {
		t.Fatal("discovered neighbor must not be in any wave")
	}
}

func TestBuildUpgradePlan_MultipleComponents(t *testing.T) {
	results := map[string]*lldp.ParseResult{
		"hub1":   {Neighbors: []*lldp.Neighbor{{Identity: "leaf1a"}, {Identity: "leaf1b"}}},
		"leaf1a": {Neighbors: []*lldp.Neighbor{}},
		"leaf1b": {Neighbors: []*lldp.Neighbor{}},
		"hub2":   {Neighbors: []*lldp.Neighbor{{Identity: "leaf2a"}}},
		"leaf2a": {Neighbors: []*lldp.Neighbor{}},
	}
	orderedHosts := []string{"hub1", "leaf1a", "leaf1b", "hub2", "leaf2a"}
	plan := buildPlanForTest(t, results, orderedHosts)

	if len(plan.Waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(plan.Waves))
	}
	if len(plan.Waves[0].Devices) != 3 {
		t.Fatalf("expected 3 leaves in wave 1, got %v", plan.Waves[0].Devices)
	}
	if len(plan.Waves[1].Devices) != 2 {
		t.Fatalf("expected 2 hubs in wave 2, got %v", plan.Waves[1].Devices)
	}
}

func TestBuildUpgradePlan_EmptyUpgradeable(t *testing.T) {
	plan := buildPlanForTest(t, map[string]*lldp.ParseResult{}, []string{})
	if len(plan.Waves) != 0 {
		t.Fatalf("expected 0 waves, got %d", len(plan.Waves))
	}
	if len(plan.Excluded) != 0 {
		t.Fatalf("expected 0 excluded, got %v", plan.Excluded)
	}
}

func TestBuildUpgradePlan_NilTopology(t *testing.T) {
	_, err := BuildUpgradePlan(nil)
	if err == nil {
		t.Fatal("expected error for nil topology")
	}
}

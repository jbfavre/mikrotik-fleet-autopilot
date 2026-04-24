package lldp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture loads a test fixture file from testdata
func loadFixture(t *testing.T, filename string) string {
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to load fixture %s: %v", filename, err)
	}
	return string(data)
}

// Unit Tests

func TestTokenizeKeyValue_QuotedValue(t *testing.T) {
	record := `identity="router30-rb4011.jbfav.re" platform="MikroTik"`
	fields, err := tokenizeKeyValue(record)
	if err != nil {
		t.Fatalf("tokenizeKeyValue failed: %v", err)
	}
	if fields["identity"] != "router30-rb4011.jbfav.re" {
		t.Errorf("identity = %q, want router30-rb4011.jbfav.re", fields["identity"])
	}
	if fields["platform"] != "MikroTik" {
		t.Errorf("platform = %q, want MikroTik", fields["platform"])
	}
}

func TestTokenizeKeyValue_UnquotedValue(t *testing.T) {
	record := `age=3s unpack=none platform=MikroTik`
	fields, err := tokenizeKeyValue(record)
	if err != nil {
		t.Fatalf("tokenizeKeyValue failed: %v", err)
	}
	if fields["age"] != "3s" {
		t.Errorf("age = %q, want 3s", fields["age"])
	}
	if fields["unpack"] != "none" {
		t.Errorf("unpack = %q, want none", fields["unpack"])
	}
	if fields["platform"] != "MikroTik" {
		t.Errorf("platform = %q, want MikroTik", fields["platform"])
	}
}

func TestTokenizeKeyValue_MultilineValue(t *testing.T) {
	record := `system-description="MikroTik RouterOS 7.22.2 (stable) 2026-04-22 08:03:57 
                   RB4011iGS+" platform="MikroTik"`
	fields, err := tokenizeKeyValue(record)
	if err != nil {
		t.Fatalf("tokenizeKeyValue failed: %v", err)
	}
	// Should normalize whitespace
	desc := fields["system-description"]
	if !strings.Contains(desc, "MikroTik RouterOS 7.22.2") {
		t.Errorf("system-description missing expected content: %q", desc)
	}
	if !strings.Contains(desc, "RB4011iGS+") {
		t.Errorf("system-description missing RB4011iGS+: %q", desc)
	}
}

func TestSplitRecords(t *testing.T) {
	raw := ` 0 interface=sfp-sfpplus3,br-lan identity="router30" 
   platform="MikroTik" 
 1 interface=sfp-sfpplus7,br-lan identity="router70"`

	records := splitRecords(raw)
	if len(records) != 2 {
		t.Errorf("splitRecords returned %d records, want 2", len(records))
	}

	if !strings.Contains(records[0], "router30") {
		t.Errorf("record[0] missing router30")
	}
	if !strings.Contains(records[1], "router70") {
		t.Errorf("record[1] missing router70")
	}
}

func TestExtractLocalInterface_SinglePart(t *testing.T) {
	iface := extractLocalInterface("sfp1")
	if iface != "sfp1" {
		t.Errorf("extractLocalInterface = %q, want sfp1", iface)
	}
}

func TestExtractLocalInterface_ChainWithBridge(t *testing.T) {
	iface := extractLocalInterface("sfp-sfpplus3,br-lan")
	if iface != "sfp-sfpplus3" {
		t.Errorf("extractLocalInterface = %q, want sfp-sfpplus3", iface)
	}
}

func TestExtractLocalInterface_BondChain(t *testing.T) {
	iface := extractLocalInterface("ether9,bond0,br-lan")
	if iface != "ether9" {
		t.Errorf("extractLocalInterface = %q, want ether9", iface)
	}
}

func TestExtractLocalInterface_Empty(t *testing.T) {
	iface := extractLocalInterface("")
	if iface != "" {
		t.Errorf("extractLocalInterface = %q, want empty", iface)
	}
}

func TestParseCommaSeparated_Single(t *testing.T) {
	caps := parseCommaSeparated("bridge")
	if len(caps) != 1 || caps[0] != "bridge" {
		t.Errorf("parseCommaSeparated = %v, want [bridge]", caps)
	}
}

func TestParseCommaSeparated_Multiple(t *testing.T) {
	caps := parseCommaSeparated("cdp,lldp,mndp")
	if len(caps) != 3 {
		t.Errorf("parseCommaSeparated returned %d elements, want 3", len(caps))
	}
	if caps[0] != "cdp" || caps[1] != "lldp" || caps[2] != "mndp" {
		t.Errorf("parseCommaSeparated = %v, want [cdp lldp mndp]", caps)
	}
}

func TestParseCommaSeparated_Empty(t *testing.T) {
	caps := parseCommaSeparated("")
	if len(caps) != 0 {
		t.Errorf("parseCommaSeparated = %v, want []", caps)
	}
}

func TestDetectSourceIdentity(t *testing.T) {
	raw := "[admin@router1] > /ip neighbor"
	identity := DetectSourceIdentity(raw)
	if identity != "router1" {
		t.Errorf("DetectSourceIdentity = %q, want router1", identity)
	}
}

func TestDetectSourceIdentity_NoPrompt(t *testing.T) {
	raw := "0 identity=device platform=MikroTik"
	identity := DetectSourceIdentity(raw)
	if identity != "" {
		t.Errorf("DetectSourceIdentity = %q, want empty", identity)
	}
}

func TestParseNeighbor_MissingIdentity(t *testing.T) {
	fields := map[string]string{
		"platform": "MikroTik",
		"version":  "7.22.2",
	}
	_, err := newNeighbor(0, fields)
	if err == nil {
		t.Error("newNeighbor should error when identity is missing")
	}
}

func TestParseNeighbor_AllFields(t *testing.T) {
	fields := map[string]string{
		"identity":            "router30-rb4011.jbfav.re",
		"interface":           "sfp-sfpplus3,br-lan",
		"interface-name":      "br-lan/sfp-sfpplus1",
		"platform":            "MikroTik",
		"version":             "7.22.2 (stable) 2026-04-22 08:03:57",
		"board":               "RB4011iGS+",
		"address":             "fe80::a55:31ff:fe5a:abc6",
		"address6":            "fe80::a55:31ff:fe5a:abc6",
		"mac-address":         "08:55:31:5A:AB:D0",
		"system-description":  "MikroTik RouterOS 7.22.2",
		"system-caps":         "bridge,router",
		"system-caps-enabled": "bridge,router",
		"discovered-by":       "lldp",
		"age":                 "3s",
		"unpack":              "none",
		"ipv6":                "yes",
	}

	n, err := newNeighbor(0, fields)
	if err != nil {
		t.Fatalf("newNeighbor failed: %v", err)
	}

	if n.Index != 0 {
		t.Errorf("Index = %d, want 0", n.Index)
	}
	if n.Identity != "router30-rb4011.jbfav.re" {
		t.Errorf("Identity = %q", n.Identity)
	}
	if n.LocalInterface != "sfp-sfpplus3" {
		t.Errorf("LocalInterface = %q, want sfp-sfpplus3", n.LocalInterface)
	}
	if n.LocalInterfaceChain != "sfp-sfpplus3,br-lan" {
		t.Errorf("LocalInterfaceChain = %q", n.LocalInterfaceChain)
	}
	if n.Board != "RB4011iGS+" {
		t.Errorf("Board = %q", n.Board)
	}
	if !n.IPv6Enabled {
		t.Error("IPv6Enabled should be true")
	}
	if len(n.SystemCaps) != 2 || n.SystemCaps[0] != "bridge" || n.SystemCaps[1] != "router" {
		t.Errorf("SystemCaps = %v", n.SystemCaps)
	}
}

// Integration Tests

func TestParseNeighbors_EmptyInput(t *testing.T) {
	result, err := ParseNeighbors("")
	if err != nil {
		t.Fatalf("ParseNeighbors failed: %v", err)
	}
	if result == nil {
		t.Error("ParseNeighbors returned nil")
	}
	if len(result.Neighbors) != 0 {
		t.Errorf("expected 0 neighbors, got %d", len(result.Neighbors))
	}
}

func TestParseNeighbors_Router1(t *testing.T) {
	raw := loadFixture(t, "router1.txt")
	result, err := ParseNeighbors(raw)
	if err != nil {
		t.Fatalf("ParseNeighbors failed: %v", err)
	}

	if len(result.Neighbors) != 2 {
		t.Fatalf("expected 2 neighbors, got %d", len(result.Neighbors))
	}

	// Neighbor 0
	n0 := result.Neighbors[0]
	if n0.Index != 0 {
		t.Errorf("n0.Index = %d, want 0", n0.Index)
	}
	if n0.LocalInterface != "sfp-sfpplus3" {
		t.Errorf("n0.LocalInterface = %q, want sfp-sfpplus3", n0.LocalInterface)
	}
	if n0.Identity != "router30-rb4011.jbfav.re" {
		t.Errorf("n0.Identity = %q", n0.Identity)
	}
	if n0.Address6 != "fe80::a55:31ff:fe5a:abc6" {
		t.Errorf("n0.Address6 mismatch")
	}
	if n0.MacAddress != "08:55:31:5A:AB:D0" {
		t.Errorf("n0.MacAddress mismatch")
	}
	if n0.Board != "RB4011iGS+" {
		t.Errorf("n0.Board = %q", n0.Board)
	}
	if !sliceContains(n0.SystemCaps, "bridge") {
		t.Error("n0.SystemCaps missing bridge")
	}
	if !sliceContains(n0.SystemCaps, "router") {
		t.Error("n0.SystemCaps missing router")
	}
	if !sliceContains(n0.DiscoveredBy, "lldp") {
		t.Error("n0.DiscoveredBy missing lldp")
	}
	if n0.Uptime != "" {
		t.Errorf("n0.Uptime should be empty, got %q", n0.Uptime)
	}

	// Neighbor 1
	n1 := result.Neighbors[1]
	if n1.LocalInterface != "sfp-sfpplus7" {
		t.Errorf("n1.LocalInterface = %q, want sfp-sfpplus7", n1.LocalInterface)
	}
	if n1.Identity != "router70-rb4011.jbfav.re" {
		t.Errorf("n1.Identity = %q", n1.Identity)
	}
}

func TestParseNeighbors_Router30(t *testing.T) {
	raw := loadFixture(t, "router30.txt")
	result, err := ParseNeighbors(raw)
	if err != nil {
		t.Fatalf("ParseNeighbors failed: %v", err)
	}

	if len(result.Neighbors) != 3 {
		t.Fatalf("expected 3 neighbors, got %d", len(result.Neighbors))
	}

	// Neighbor 0: has uptime and software-id
	n0 := result.Neighbors[0]
	if n0.LocalInterface != "ether9" {
		t.Errorf("n0.LocalInterface = %q, want ether9", n0.LocalInterface)
	}
	if n0.Identity != "router31-capac.jbfav.re" {
		t.Errorf("n0.Identity mismatch")
	}
	if n0.Uptime != "4h57m43s" {
		t.Errorf("n0.Uptime = %q, want 4h57m43s", n0.Uptime)
	}
	if n0.SoftwareID != "3SH0-DWT8" {
		t.Errorf("n0.SoftwareID = %q, want 3SH0-DWT8", n0.SoftwareID)
	}
	if !sliceContains(n0.DiscoveredBy, "cdp") {
		t.Error("n0.DiscoveredBy missing cdp")
	}
	if !sliceContains(n0.DiscoveredBy, "lldp") {
		t.Error("n0.DiscoveredBy missing lldp")
	}
	if !sliceContains(n0.DiscoveredBy, "mndp") {
		t.Error("n0.DiscoveredBy missing mndp")
	}

	// Neighbor 1: duplicate peer but different interface
	n1 := result.Neighbors[1]
	if n1.LocalInterface != "ether10" {
		t.Errorf("n1.LocalInterface = %q, want ether10", n1.LocalInterface)
	}
	if n1.Identity != "router31-capac.jbfav.re" {
		t.Errorf("n1.Identity mismatch (should be same peer as n0)")
	}

	// Neighbor 2: different peer
	n2 := result.Neighbors[2]
	if n2.LocalInterface != "sfp-sfpplus1" {
		t.Errorf("n2.LocalInterface = %q, want sfp-sfpplus1", n2.LocalInterface)
	}
	if n2.Identity != "core.jbfav.re" {
		t.Errorf("n2.Identity = %q, want core.jbfav.re", n2.Identity)
	}
	if n2.Uptime != "" {
		t.Errorf("n2.Uptime should be empty (not in fixture), got %q", n2.Uptime)
	}
	if n2.SoftwareID != "" {
		t.Errorf("n2.SoftwareID should be empty (not in fixture), got %q", n2.SoftwareID)
	}
	// n2 should only have lldp, not cdp/mndp
	if !sliceContains(n2.DiscoveredBy, "lldp") {
		t.Error("n2.DiscoveredBy missing lldp")
	}
	if sliceContains(n2.DiscoveredBy, "cdp") {
		t.Error("n2.DiscoveredBy should not have cdp")
	}
}

func TestParseNeighbors_Router70(t *testing.T) {
	raw := loadFixture(t, "router70.txt")
	result, err := ParseNeighbors(raw)
	if err != nil {
		t.Fatalf("ParseNeighbors failed: %v", err)
	}

	if len(result.Neighbors) != 3 {
		t.Fatalf("expected 3 neighbors, got %d", len(result.Neighbors))
	}

	// Verify structure similar to router30
	n0 := result.Neighbors[0]
	if n0.LocalInterface != "ether9" {
		t.Errorf("n0.LocalInterface = %q, want ether9", n0.LocalInterface)
	}
	if n0.Identity != "router71-capac.jbfav.re" {
		t.Errorf("n0.Identity mismatch")
	}
}

func TestParseNeighbors_Router90(t *testing.T) {
	raw := loadFixture(t, "router90.txt")
	result, err := ParseNeighbors(raw)
	if err != nil {
		t.Fatalf("ParseNeighbors failed: %v", err)
	}

	if len(result.Neighbors) != 1 {
		t.Fatalf("expected 1 neighbor, got %d", len(result.Neighbors))
	}

	n0 := result.Neighbors[0]
	if n0.LocalInterface != "sfp1" {
		t.Errorf("n0.LocalInterface = %q, want sfp1", n0.LocalInterface)
	}
	if n0.Identity != "core.jbfav.re" {
		t.Errorf("n0.Identity = %q, want core.jbfav.re", n0.Identity)
	}
	if n0.Board != "CCR2004-1G-12S+2XS" {
		t.Errorf("n0.Board mismatch")
	}
}

// Helper Tests

func TestByLocalInterface(t *testing.T) {
	raw := loadFixture(t, "router30.txt")
	result, _ := ParseNeighbors(raw)

	grouped := ByLocalInterface(result.Neighbors)

	if len(grouped) != 3 {
		t.Errorf("expected 3 groups, got %d", len(grouped))
	}
	if len(grouped["ether9"]) != 1 {
		t.Errorf("ether9 should have 1 neighbor, got %d", len(grouped["ether9"]))
	}
	if len(grouped["ether10"]) != 1 {
		t.Errorf("ether10 should have 1 neighbor, got %d", len(grouped["ether10"]))
	}
	if len(grouped["sfp-sfpplus1"]) != 1 {
		t.Errorf("sfp-sfpplus1 should have 1 neighbor, got %d", len(grouped["sfp-sfpplus1"]))
	}
}

func TestByIdentity(t *testing.T) {
	raw := loadFixture(t, "router30.txt")
	result, _ := ParseNeighbors(raw)

	grouped := ByIdentity(result.Neighbors)

	if len(grouped) != 2 {
		t.Errorf("expected 2 device identities, got %d", len(grouped))
	}
	if len(grouped["router31-capac.jbfav.re"]) != 2 {
		t.Errorf("router31-capac.jbfav.re should have 2 neighbors (multi-interface), got %d", len(grouped["router31-capac.jbfav.re"]))
	}
	if len(grouped["core.jbfav.re"]) != 1 {
		t.Errorf("core.jbfav.re should have 1 neighbor, got %d", len(grouped["core.jbfav.re"]))
	}
}

func TestFilterByDiscoveryProtocol_LLDP(t *testing.T) {
	raw := loadFixture(t, "router30.txt")
	result, _ := ParseNeighbors(raw)

	lldpOnly := FilterByDiscoveryProtocol(result.Neighbors, "lldp")
	if len(lldpOnly) != 3 {
		t.Errorf("expected 3 LLDP neighbors, got %d", len(lldpOnly))
	}
}

func TestFilterByDiscoveryProtocol_CDP(t *testing.T) {
	raw := loadFixture(t, "router30.txt")
	result, _ := ParseNeighbors(raw)

	cdpOnly := FilterByDiscoveryProtocol(result.Neighbors, "cdp")
	if len(cdpOnly) != 2 {
		t.Errorf("expected 2 CDP neighbors (router31 peers), got %d", len(cdpOnly))
	}
}

// Helper function for test assertions
func sliceContains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}

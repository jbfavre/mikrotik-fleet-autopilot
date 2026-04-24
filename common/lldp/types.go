package lldp

// Neighbor represents a single LLDP neighbor discovered by a RouterOS device
type Neighbor struct {
	// Record index from RouterOS output (0, 1, 2, ...)
	Index int

	// Local interface where neighbor was discovered
	LocalInterface      string // extracted first part: "sfp-sfpplus3"
	LocalInterfaceChain string // full chain: "sfp-sfpplus3,br-lan"

	// Remote interface information
	RemoteInterface string // interface-name from neighbor perspective

	// Neighbor device identity
	Identity string // FQDN of neighbor device
	Platform string // typically "MikroTik"
	Version  string // RouterOS version
	Board    string // hardware model (RB4011iGS+, CCR2004-1G-12S+2XS, etc.)

	// Network addressing
	Address    string // IPv4 address (fe80:: prefix for link-local)
	Address6   string // IPv6 address
	MacAddress string // hardware MAC address

	// Device capabilities and protocols
	SystemDescription string   // hardware and OS description
	SystemCaps        []string // capabilities: bridge, router, etc.
	SystemCapsEnabled []string // enabled capabilities
	DiscoveredBy      []string // protocols used: lldp, cdp, mndp

	// Timing and diagnostics
	Age         string // time since last heard (3s, 0s, etc.)
	Uptime      string // neighbor device uptime (optional: 4h57m43s)
	SoftwareID  string // unique software ID (optional: 3SH0-DWT8)
	IPv6Enabled bool   // whether IPv6 is enabled
	Unpack      string // unpack status (typically "none")
}

// ParseResult holds the output of ParseNeighbors
type ParseResult struct {
	SourceIdentity string
	Neighbors      []*Neighbor
	Warnings       []string
}

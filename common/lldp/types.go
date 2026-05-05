package lldp

// Neighbor represents a single LLDP neighbor discovered by a RouterOS device
type Neighbor struct {
	// Parse order of this neighbor within the parsed RouterOS output.
	// This is not guaranteed to match any numeric record prefix emitted by RouterOS.
	Index int

	// Local interface where neighbor was discovered
	LocalInterface      string // extracted first part: "sfp-sfpplus3"
	LocalInterfaceChain string // full chain: "sfp-sfpplus3,br-lan"

	// Remote interface information
	RemoteInterface      string // extracted last part: "ether1"
	RemoteInterfaceChain string // full chain: "br-lan/ether1"

	// Neighbor device identity
	Identity string // FQDN of neighbor device
	Platform string // typically "MikroTik"
	Version  string // RouterOS version
	Board    string // hardware model (RB4011iGS+, CCR2004-1G-12S+2XS, etc.)

	// Network addressing
	Address    string // RouterOS "address" field; may contain either IPv4 or IPv6 (including link-local fe80:: addresses)
	Address6   string // explicit IPv6 address field when separately present in RouterOS output
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

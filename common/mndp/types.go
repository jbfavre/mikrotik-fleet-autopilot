package mndp

// Device represents a MikroTik device discovered via MNDP.
type Device struct {
	MACAddress          string   // TLV type 1  — canonical deduplication key (lower-case hex string, e.g. "aa:bb:cc:dd:ee:ff")
	Identity            string   // TLV type 5  — identity key used in discovery/topology output (may be disambiguated as "<identity> #<n>")
	BaseIdentity        string   // original TLV type 5 identity value (never disambiguated)
	Version             string   // TLV type 7
	Platform            string   // TLV type 8  ("MikroTik")
	Uptime              uint32   // TLV type 10 — seconds (4-byte LE)
	SoftwareID          string   // TLV type 11
	Board               string   // TLV type 12
	Unpack              bool     // TLV type 14 — whether the device supports unpacking
	IPv6Address         string   // TLV type 15 — link-local or global IPv6 (not yet used)
	SourceInterfaceName string   // TLV type 16 — interface name on the remote device
	IPv4Address         string   // TLV type 17 — last observed IPv4 from a packet
	IPv4Addresses       []string // all observed IPv4 addresses for this device across sightings
	CanonicalIPv4       string   // canonical IPv4 selected from DNS lookup on BaseIdentity
	InterfaceName       string   // local NIC on which the UDP response arrived (set by listener, not parsed)
}

package mndp

// Device represents a MikroTik device discovered via MNDP.
type Device struct {
	MACAddress    string // TLV type 1  — canonical deduplication key (lower-case hex string, e.g. "aa:bb:cc:dd:ee:ff")
	Identity      string // TLV type 5  — matches LLDP neighbor identity
	Version       string // TLV type 7
	Platform      string // TLV type 8  ("MikroTik")
	Uptime        uint32 // TLV type 10 — seconds (4-byte LE)
	SoftwareID    string // TLV type 11
	Board         string // TLV type 12
	IPv4Address   string // TLV type 15, length==4 only; IPv6 (length==16) is silently skipped
	InterfaceName string // local NIC on which the UDP response arrived
}

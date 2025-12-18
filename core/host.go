package core

import (
	"net"
	"strings"
)

// HostInfo is the single source of truth for host connection details
type HostInfo struct {
	// User input
	Original string // What user provided (e.g., "router1", "192.168.1.1:2222")
	Type     string // "ip", "fqdn", "hostname" (for smart decisions)

	// Connection details (merged from input + ssh_config)
	Hostname string // Final hostname/IP to connect to
	Port     string // Final port (from input or ssh_config or default "22")
	User     string // From ssh_config or empty

	// For filename generation
	ShortName string // Smart extraction based on type:
	//   IP: full IP (192.168.1.1)
	//   FQDN: strip domain (router1.home.local → router1)
	//   Hostname: as-is (router1 → router1)

	// SSH config details (optional, from ssh_config)
	IdentityFile             string
	IdentitiesOnly           string
	ForwardAgent             string
	HostkeyAlgorithms        string
	PubkeyAcceptedAlgorithms string
}

// ParseHost analyzes a host string and returns initial HostInfo
// This is the reference that will be enriched by ssh_config
func ParseHost(host string) *HostInfo {
	info := &HostInfo{
		Original: host,
		Port:     "22", // Default port
	}

	// Check if port is specified in the input (e.g., "192.168.1.1:2222")
	hostPart := host
	if strings.Contains(host, ":") {
		h, p, err := net.SplitHostPort(host)
		if err == nil {
			hostPart = h
			info.Port = p
		}
	}

	// Determine host type and set initial values
	if IsIPAddress(hostPart) {
		// IP address (IPv4 or IPv6)
		info.Type = "ip"
		info.Hostname = hostPart
		info.ShortName = hostPart // Keep full IP for filename
	} else if strings.Contains(hostPart, ".") {
		// FQDN (has dots, not an IP)
		info.Type = "fqdn"
		info.Hostname = hostPart
		// Strip domain for short name (e.g., router1.home.local → router1)
		if idx := strings.Index(hostPart, "."); idx > 0 {
			info.ShortName = hostPart[:idx]
		} else {
			info.ShortName = hostPart
		}
	} else {
		// Simple hostname (no dots, not an IP)
		info.Type = "hostname"
		info.Hostname = hostPart
		info.ShortName = hostPart // Use as-is
	}

	return info
}

// IsIPAddress checks if string is valid IPv4/IPv6
func IsIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

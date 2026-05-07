package lldp

import "slices"

// ByLocalInterface groups neighbors by the local interface where they were discovered
func ByLocalInterface(neighbors []*Neighbor) map[string][]*Neighbor {
	result := make(map[string][]*Neighbor)
	for _, n := range neighbors {
		if n.LocalInterface != "" {
			result[n.LocalInterface] = append(result[n.LocalInterface], n)
		}
	}
	return result
}

// ByIdentity groups neighbors by remote device identity (FQDN)
func ByIdentity(neighbors []*Neighbor) map[string][]*Neighbor {
	result := make(map[string][]*Neighbor)
	for _, n := range neighbors {
		if n.Identity != "" {
			result[n.Identity] = append(result[n.Identity], n)
		}
	}
	return result
}

// FilterByDiscoveryProtocol returns only neighbors discovered by a specific protocol
func FilterByDiscoveryProtocol(neighbors []*Neighbor, protocol string) []*Neighbor {
	result := make([]*Neighbor, 0)
	for _, n := range neighbors {
		if slices.Contains(n.DiscoveredBy, protocol) {
			result = append(result, n)
		}
	}
	return result
}

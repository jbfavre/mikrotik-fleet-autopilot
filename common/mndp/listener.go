package mndp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const listenReadPollInterval = 250 * time.Millisecond

// Listen sends an MNDP probe on ifaceName (or all eligible interfaces when empty)
// and collects responses for the duration of timeout.
// Devices are grouped by identity and deduplicated by MAC address inside each identity group.
// If multiple MAC addresses share the same identity (multi-homed device), the
// returned device merges observations under the same identity and keeps all
// interface records in Device.Interfaces.
// Returns the deduplicated slice, sorted by Identity.
func Listen(ctx context.Context, ifaceName string, timeout time.Duration) ([]*Device, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("mndp: timeout must be greater than 0")
	}

	var ifaces []net.Interface

	if ifaceName != "" {
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil {
			return nil, fmt.Errorf("mndp: interface %q not found: %w", ifaceName, err)
		}
		ifaces = []net.Interface{*iface}
	} else {
		all, err := net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("mndp: failed to list interfaces: %w", err)
		}
		for _, iface := range all {
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			if iface.Flags&net.FlagPointToPoint != 0 {
				continue
			}
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			ifaces = append(ifaces, iface)
		}
	}

	var mu sync.Mutex
	devByIdentity := make(map[string]map[string]*Device)

	var wg sync.WaitGroup

	for _, iface := range ifaces {
		addr := "0.0.0.0:5678"
		conn, err := net.ListenPacket("udp4", addr)
		if err != nil {
			if ifaceName != "" {
				return nil, fmt.Errorf("mndp: failed to bind to interface %q (%s): %w", iface.Name, addr, err)
			}
			slog.Debug("mndp: failed to bind on interface", "interface", iface.Name, "addr", addr, "error", err)
			continue
		} else {
			slog.Debug("mndp: successfull binding to interface", "interface", iface.Name, "addr", addr)
		}

		if err := SendProbe(conn); err != nil {
			if ifaceName != "" {
				_ = conn.Close()
				return nil, fmt.Errorf("mndp: failed to send probe on interface %q: %w", iface.Name, err)
			}
			slog.Debug("mndp: probe send failed", "interface", iface.Name, "error", err)
			_ = conn.Close()
			continue
		}

		deadline := time.Now().Add(timeout)
		wg.Add(1)
		go func(conn net.PacketConn, ifName string) {
			defer wg.Done()
			defer func() { _ = conn.Close() }()

			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-ctx.Done():
					_ = conn.Close()
				case <-done:
				}
			}()

			buf := make([]byte, 4096)
			for {
				readDeadline := time.Now().Add(listenReadPollInterval)
				if readDeadline.After(deadline) {
					readDeadline = deadline
				}
				if err := conn.SetReadDeadline(readDeadline); err != nil {
					slog.Debug("mndp: failed to set read deadline", "interface", ifName, "error", err)
					break
				}

				n, _, err := conn.ReadFrom(buf)
				if err != nil {
					var ne net.Error
					if errors.As(err, &ne) && ne.Timeout() {
						if !readDeadline.Before(deadline) {
							break
						}
						continue
					}
					if errors.Is(err, net.ErrClosed) {
						break
					}
					slog.Debug("mndp: read error", "interface", ifName, "error", err)
					break
				}

				dev, err := ParsePacket(buf[:n])
				if err != nil {
					slog.Debug("mndp: parse error", "interface", ifName, "error", err)
					continue
				}
				dev.InterfaceName = ifName

				mu.Lock()
				addObservation(devByIdentity, dev)
				mu.Unlock()
			}
		}(conn, iface.Name)
	}

	wg.Wait()

	return deduplicateDevices(devByIdentity), nil
}

func addObservation(devByIdentity map[string]map[string]*Device, candidate *Device) {
	identity := strings.TrimSpace(candidate.Identity)

	devByMAC, ok := devByIdentity[identity]
	if !ok {
		devByMAC = make(map[string]*Device)
		devByIdentity[identity] = devByMAC
	}

	existing, seen := devByMAC[candidate.MACAddress]
	if !seen {
		if candidate.IPv4Address != "" && len(candidate.IPv4Addresses) == 0 {
			candidate.IPv4Addresses = []string{candidate.IPv4Address}
		}
		candidate.Interfaces = mergeInterfaces(candidate.Interfaces, buildCandidateInterfaces(candidate))
		devByMAC[candidate.MACAddress] = candidate
		return
	}

	if candidate.InterfaceName != "" {
		existing.InterfaceName = candidate.InterfaceName
	}
	if candidate.SourceInterfaceName != "" {
		existing.SourceInterfaceName = candidate.SourceInterfaceName
	}
	if candidate.IPv4Address != "" {
		existing.IPv4Address = candidate.IPv4Address
		existing.IPv4Addresses = appendUnique(existing.IPv4Addresses, candidate.IPv4Address)
	}
	if candidate.IPv6Address != "" {
		existing.IPv6Address = candidate.IPv6Address
	}
	existing.Interfaces = mergeInterfaces(existing.Interfaces, buildCandidateInterfaces(candidate))
}

// SendProbe sends an MNDP discovery request packet on conn.
// Exported for testability.
//
// Packet format:
//
//	[0x00 0x00]  msg-type = 0x0000 (request), LE
//	[0x00 0x00]  sequence = 0, LE
func SendProbe(conn net.PacketConn) error {
	dst, err := net.ResolveUDPAddr("udp4", "255.255.255.255:5678")
	if err != nil {
		return fmt.Errorf("mndp: failed to resolve broadcast addr: %w", err)
	}
	probe := []byte{0x00, 0x00, 0x00, 0x00}
	if _, err := conn.WriteTo(probe, dst); err != nil {
		return fmt.Errorf("mndp: failed to send probe: %w", err)
	}
	return nil
}

func deduplicateDevices(devByIdentity map[string]map[string]*Device) []*Device {
	identities := make([]string, 0, len(devByIdentity))
	for identity := range devByIdentity {
		identities = append(identities, identity)
	}
	sort.Strings(identities)

	devices := make([]*Device, 0, len(identities))
	for _, identity := range identities {
		byMAC := devByIdentity[identity]
		macs := make([]string, 0, len(byMAC))
		for mac := range byMAC {
			macs = append(macs, mac)
		}
		sort.Strings(macs)

		merged := byMAC[macs[0]]
		merged.Identity = identity
		merged.Interfaces = mergeInterfaces(merged.Interfaces, buildCandidateInterfaces(merged))
		for _, mac := range macs[1:] {
			d := byMAC[mac]
			merged.Interfaces = mergeInterfaces(merged.Interfaces, buildCandidateInterfaces(d))
			merged.IPv4Addresses = mergeIPv4Addresses(merged.IPv4Addresses, d.IPv4Addresses, d.IPv4Address)
			if d.IPv4Address != "" {
				merged.IPv4Address = d.IPv4Address
			}
			if d.IPv6Address != "" {
				merged.IPv6Address = d.IPv6Address
			}
			if d.SourceInterfaceName != "" {
				merged.SourceInterfaceName = d.SourceInterfaceName
			}
			if d.InterfaceName != "" {
				merged.InterfaceName = d.InterfaceName
			}
			if metadataConflict(merged.Board, d.Board) || metadataConflict(merged.Version, d.Version) || metadataConflict(merged.Platform, d.Platform) {
				slog.Warn("mndp: inconsistent metadata across interfaces for identity",
					"identity", identity,
					"mac_a", merged.MACAddress,
					"mac_b", d.MACAddress,
					"board_a", merged.Board,
					"board_b", d.Board,
					"version_a", merged.Version,
					"version_b", d.Version,
					"platform_a", merged.Platform,
					"platform_b", d.Platform,
				)
			}
		}
		devices = append(devices, merged)
	}

	return devices
}

func appendUnique(values []string, candidate string) []string {
	if candidate == "" {
		return values
	}
	if slices.Contains(values, candidate) {
		return values
	}
	return append(values, candidate)
}

func metadataConflict(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && right != "" && left != right
}

func buildCandidateInterfaces(device *Device) []InterfaceRecord {
	if device == nil {
		return nil
	}
	if len(device.Interfaces) > 0 {
		return device.Interfaces
	}
	record := InterfaceRecord{
		MACAddress:          strings.TrimSpace(device.MACAddress),
		IPv4Address:         strings.TrimSpace(device.IPv4Address),
		InterfaceName:       strings.TrimSpace(device.InterfaceName),
		SourceInterfaceName: strings.TrimSpace(device.SourceInterfaceName),
	}
	if record.MACAddress == "" && record.IPv4Address == "" && record.InterfaceName == "" && record.SourceInterfaceName == "" {
		return nil
	}
	return []InterfaceRecord{record}
}

func mergeInterfaces(existing []InterfaceRecord, candidates []InterfaceRecord) []InterfaceRecord {
	for _, candidate := range candidates {
		if candidate.MACAddress == "" && candidate.IPv4Address == "" && candidate.InterfaceName == "" && candidate.SourceInterfaceName == "" {
			continue
		}
		duplicate := false
		for _, record := range existing {
			if record == candidate {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, candidate)
		}
	}
	sort.Slice(existing, func(i, j int) bool {
		left := existing[i]
		right := existing[j]
		if left.MACAddress != right.MACAddress {
			return left.MACAddress < right.MACAddress
		}
		if left.IPv4Address != right.IPv4Address {
			return left.IPv4Address < right.IPv4Address
		}
		if left.InterfaceName != right.InterfaceName {
			return left.InterfaceName < right.InterfaceName
		}
		return left.SourceInterfaceName < right.SourceInterfaceName
	})
	return existing
}

func mergeIPv4Addresses(existing []string, fromSlice []string, direct string) []string {
	for _, ip := range fromSlice {
		existing = appendUnique(existing, ip)
	}
	return appendUnique(existing, direct)
}

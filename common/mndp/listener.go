package mndp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

const listenReadPollInterval = 250 * time.Millisecond

// Listen sends an MNDP probe on ifaceName (or all eligible interfaces when empty)
// and collects responses for the duration of timeout.
// Devices are deduplicated by MACAddress (last-seen wins).
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
	devByMAC := make(map[string]*Device)

	var wg sync.WaitGroup

	for _, iface := range ifaces {
		//ipv4, err := firstIPv4(iface)
		//if err != nil {
		//	if ifaceName != "" {
		//		return nil, fmt.Errorf("mndp: interface %q has no usable IPv4 address: %w", iface.Name, err)
		//	}
		//	slog.Debug("mndp: skipping interface (no IPv4)", "interface", iface.Name, "error", err)
		//	continue
		//}

		addr := "0.0.0.0:5678"
		conn, err := net.ListenPacket("udp4", addr)
		if err != nil {
			if ifaceName != "" {
				return nil, fmt.Errorf("mndp: failed to bind to interface %q (%s): %w", iface.Name, addr, err)
			}
			slog.Debug("mndp: failed to bind on interface", "interface", iface.Name, "addr", addr, "error", err)
			continue
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
				existing, seen := devByMAC[dev.MACAddress]
				if !seen || dev.IPv4Address != "" || existing.IPv4Address == "" {
					devByMAC[dev.MACAddress] = dev
				}
				mu.Unlock()
			}
		}(conn, iface.Name)
	}

	wg.Wait()

	return deduplicateDevices(devByMAC), nil
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

// firstIPv4 returns the string representation of the first IPv4 unicast
// address assigned to iface, or an error if none is found.
func firstIPv4(iface net.Interface) (string, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		switch v := addr.(type) {
		case *net.IPNet:
			ip := v.IP.To4()
			if ip != nil {
				return ip.String(), nil
			}
		case *net.IPAddr:
			ip := v.IP.To4()
			if ip != nil {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no IPv4 address found on interface %q", iface.Name)
}

// deduplicateDevices takes the last-seen-wins map, builds a slice, and sorts it by Identity.
func deduplicateDevices(devByMAC map[string]*Device) []*Device {
	devices := make([]*Device, 0, len(devByMAC))
	for _, d := range devByMAC {
		devices = append(devices, d)
	}
	slices.SortFunc(devices, func(a, b *Device) int {
		return strings.Compare(a.Identity, b.Identity)
	})
	return devices
}

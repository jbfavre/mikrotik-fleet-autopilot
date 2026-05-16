package mndp

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// mockPacketConn implements net.PacketConn and captures WriteTo calls.
type mockPacketConn struct {
	writtenData []byte
	writtenAddr net.Addr
}

func (m *mockPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	m.writtenData = append([]byte(nil), p...)
	m.writtenAddr = addr
	return len(p), nil
}

func (m *mockPacketConn) ReadFrom(_ []byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (m *mockPacketConn) Close() error {
	return nil
}

func (m *mockPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (m *mockPacketConn) SetDeadline(_ time.Time) error {
	return nil
}

func (m *mockPacketConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (m *mockPacketConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

// Ensure mockPacketConn satisfies net.PacketConn at compile time.
var _ net.PacketConn = (*mockPacketConn)(nil)

func TestSendProbe_WritesCorrectPayload(t *testing.T) {
	mock := &mockPacketConn{}

	if err := SendProbe(mock); err != nil {
		t.Fatalf("SendProbe() unexpected error = %v", err)
	}

	want := []byte{0x00, 0x00, 0x00, 0x00}
	if len(mock.writtenData) != 4 {
		t.Fatalf("SendProbe() wrote %d bytes, want 4", len(mock.writtenData))
	}
	for i, b := range want {
		if mock.writtenData[i] != b {
			t.Errorf("SendProbe() written[%d] = 0x%02x, want 0x%02x", i, mock.writtenData[i], b)
		}
	}
}

func TestSendProbe_SendsToBroadcast(t *testing.T) {
	mock := &mockPacketConn{}

	if err := SendProbe(mock); err != nil {
		t.Fatalf("SendProbe() unexpected error = %v", err)
	}

	udpAddr, ok := mock.writtenAddr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("SendProbe() destination addr type = %T, want *net.UDPAddr", mock.writtenAddr)
	}
	if !udpAddr.IP.Equal(net.IPv4bcast) {
		t.Errorf("SendProbe() destination IP = %v, want 255.255.255.255", udpAddr.IP)
	}
	if udpAddr.Port != 5678 {
		t.Errorf("SendProbe() destination port = %d, want 5678", udpAddr.Port)
	}
}

func TestDeduplicateDevices_MultiHomedIdentityMergedWithoutDisambiguation(t *testing.T) {
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "router.home", IPv4Address: "192.168.1.2", InterfaceName: "eth2", SourceInterfaceName: "ether2"},
			"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "router.home", IPv4Address: "192.168.1.1", InterfaceName: "eth1", SourceInterfaceName: "ether1"},
		},
		"switch.home": {
			"aa:bb:cc:dd:ee:03": {MACAddress: "aa:bb:cc:dd:ee:03", Identity: "switch.home", IPv4Address: "192.168.2.1"},
		},
	}

	result := deduplicateDevices(devByIdentity)
	if len(result) != 2 {
		t.Fatalf("deduplicateDevices() returned %d devices, want 2", len(result))
	}
	if result[0].Identity != "router.home" || result[1].Identity != "switch.home" {
		t.Fatalf("deduplicateDevices() got identities %q and %q", result[0].Identity, result[1].Identity)
	}
	if len(result[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces for merged multi-homed device, got %d", len(result[0].Interfaces))
	}
	if result[0].Interfaces[0].MACAddress != "aa:bb:cc:dd:ee:01" || result[0].Interfaces[1].MACAddress != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("expected stable interface ordering by MAC, got %q then %q", result[0].Interfaces[0].MACAddress, result[0].Interfaces[1].MACAddress)
	}
}

func TestAddObservation_MergesSameMACAndAccumulatesIPv4s(t *testing.T) {
	devByIdentity := make(map[string]map[string]*Device)

	first := &Device{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Identity:      "router.home",
		IPv4Address:   "192.168.1.10",
		IPv4Addresses: []string{"192.168.1.10"},
	}
	second := &Device{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Identity:      "router.home",
		IPv4Address:   "192.168.1.11",
		IPv4Addresses: []string{"192.168.1.11"},
	}
	duplicate := &Device{
		MACAddress:  "aa:bb:cc:dd:ee:ff",
		Identity:    "router.home",
		IPv4Address: "192.168.1.11",
	}

	addObservation(devByIdentity, first)
	addObservation(devByIdentity, second)
	addObservation(devByIdentity, duplicate)

	result := deduplicateDevices(devByIdentity)
	if len(result) != 1 {
		t.Fatalf("deduplicateDevices() returned %d devices, want 1", len(result))
	}
	if result[0].IPv4Address != "192.168.1.11" {
		t.Fatalf("expected last seen IPv4Address to be retained, got %q", result[0].IPv4Address)
	}
	if len(result[0].IPv4Addresses) != 2 {
		t.Fatalf("expected 2 unique observed IPv4 addresses, got %v", result[0].IPv4Addresses)
	}
	if result[0].IPv4Addresses[0] != "192.168.1.10" || result[0].IPv4Addresses[1] != "192.168.1.11" {
		t.Fatalf("unexpected observed IPv4 order/content: %v", result[0].IPv4Addresses)
	}
	if len(result[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interface records for merged observations, got %d", len(result[0].Interfaces))
	}
}

func TestDeduplicateDevices_WarnsOnMetadataConflict(t *testing.T) {
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "router.home", Board: "RB5009", Version: "7.18.2", Platform: "MikroTik"},
			"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "router.home", Board: "CCR2116", Version: "7.18.2", Platform: "MikroTik"},
		},
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	deduplicateDevices(devByIdentity)
	if !strings.Contains(logs.String(), "inconsistent metadata across interfaces for identity") {
		t.Fatalf("expected warning log about metadata conflict, got %q", logs.String())
	}
}

func TestListen_RejectsNonPositiveTimeout(t *testing.T) {
	_, err := Listen(context.Background(), "", 0)
	if err == nil {
		t.Fatal("Listen() expected error for non-positive timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be greater than 0") {
		t.Fatalf("Listen() error = %v, want timeout validation error", err)
	}
}

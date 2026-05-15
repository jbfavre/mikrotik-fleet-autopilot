package mndp

import (
	"context"
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

func TestDeduplicateDevices_IdentityCollisionReturnsError(t *testing.T) {
	devByIdentity := map[string]map[string]*Device{
		"router.home": {
			"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "router.home", BaseIdentity: "router.home"},
			"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "router.home", BaseIdentity: "router.home"},
		},
		"switch.home": {
			"aa:bb:cc:dd:ee:03": {MACAddress: "aa:bb:cc:dd:ee:03", Identity: "switch.home", BaseIdentity: "switch.home"},
		},
	}

	_, err := deduplicateDevices(devByIdentity)
	if err == nil {
		t.Fatal("deduplicateDevices() expected duplicate identity error, got nil")
	}
	if !strings.Contains(err.Error(), `duplicate identity "router.home"`) {
		t.Fatalf("expected duplicate identity error, got %v", err)
	}
}

func TestAddObservation_MergesSameMACAndAccumulatesIPv4s(t *testing.T) {
	devByIdentity := make(map[string]map[string]*Device)

	first := &Device{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Identity:      "router.home",
		BaseIdentity:  "router.home",
		IPv4Address:   "192.168.1.10",
		IPv4Addresses: []string{"192.168.1.10"},
	}
	second := &Device{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		Identity:      "router.home",
		BaseIdentity:  "router.home",
		IPv4Address:   "192.168.1.11",
		IPv4Addresses: []string{"192.168.1.11"},
	}
	duplicate := &Device{
		MACAddress:   "aa:bb:cc:dd:ee:ff",
		Identity:     "router.home",
		BaseIdentity: "router.home",
		IPv4Address:  "192.168.1.11",
	}

	addObservation(devByIdentity, first)
	addObservation(devByIdentity, second)
	addObservation(devByIdentity, duplicate)

	result, err := deduplicateDevices(devByIdentity)
	if err != nil {
		t.Fatalf("deduplicateDevices() unexpected error = %v", err)
	}
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

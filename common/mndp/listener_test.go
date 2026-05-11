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

func TestDeduplicateDevices_LastSeenWins(t *testing.T) {
	mac := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}

	pkt1 := buildTestPacket(mac, "first-identity", "", "", 0, "", "", nil, nil)
	pkt2 := buildTestPacket(mac, "last-identity", "", "", 0, "", "", nil, nil)

	devByMAC := make(map[string]*Device)

	dev1, err := ParsePacket(pkt1)
	if err != nil {
		t.Fatalf("ParsePacket(pkt1) error = %v", err)
	}
	devByMAC[dev1.MACAddress] = dev1

	dev2, err := ParsePacket(pkt2)
	if err != nil {
		t.Fatalf("ParsePacket(pkt2) error = %v", err)
	}
	devByMAC[dev2.MACAddress] = dev2 // overwrites dev1 (last-seen wins)

	result := deduplicateDevices(devByMAC)
	if len(result) != 1 {
		t.Fatalf("deduplicateDevices() returned %d devices, want 1", len(result))
	}
	if result[0].Identity != "last-identity" {
		t.Errorf("deduplicateDevices() identity = %q, want %q", result[0].Identity, "last-identity")
	}
}

func TestDeduplicateDevices_SortedByIdentity(t *testing.T) {
	devByMAC := map[string]*Device{
		"aa:bb:cc:dd:ee:01": {MACAddress: "aa:bb:cc:dd:ee:01", Identity: "zebra"},
		"aa:bb:cc:dd:ee:02": {MACAddress: "aa:bb:cc:dd:ee:02", Identity: "apple"},
		"aa:bb:cc:dd:ee:03": {MACAddress: "aa:bb:cc:dd:ee:03", Identity: "mango"},
	}

	result := deduplicateDevices(devByMAC)
	if len(result) != 3 {
		t.Fatalf("deduplicateDevices() returned %d devices, want 3", len(result))
	}
	if result[0].Identity != "apple" || result[1].Identity != "mango" || result[2].Identity != "zebra" {
		t.Errorf("deduplicateDevices() not sorted: got %q, %q, %q",
			result[0].Identity, result[1].Identity, result[2].Identity)
	}
}

func TestShouldReplaceDevice(t *testing.T) {
	withIPv4 := &Device{IPv4Address: "192.168.1.10"}
	withoutIPv4 := &Device{}

	tests := []struct {
		name      string
		seen      bool
		existing  *Device
		candidate *Device
		want      bool
	}{
		{
			name:      "unseen device is always accepted",
			seen:      false,
			existing:  nil,
			candidate: withoutIPv4,
			want:      true,
		},
		{
			name:      "candidate with IPv4 replaces existing without IPv4",
			seen:      true,
			existing:  withoutIPv4,
			candidate: withIPv4,
			want:      true,
		},
		{
			name:      "candidate without IPv4 does not replace existing with IPv4",
			seen:      true,
			existing:  withIPv4,
			candidate: withoutIPv4,
			want:      false,
		},
		{
			name:      "candidate with IPv4 replaces existing with IPv4 as newer record",
			seen:      true,
			existing:  withIPv4,
			candidate: withIPv4,
			want:      true,
		},
		{
			name:      "candidate without IPv4 replaces existing without IPv4 as newer record",
			seen:      true,
			existing:  withoutIPv4,
			candidate: withoutIPv4,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldReplaceDevice(tt.seen, tt.existing, tt.candidate)
			if got != tt.want {
				t.Fatalf("shouldReplaceDevice() = %v, want %v", got, tt.want)
			}
		})
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

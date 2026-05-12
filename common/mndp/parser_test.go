package mndp

import (
	"encoding/binary"
	"testing"
)

// buildTestPacket constructs a minimal valid MNDP response packet for testing.
// mac must be 6 bytes; identity must be non-empty.
func buildTestPacket(mac []byte, identity, version, platform string, uptime uint32, softwareID, board string, ipv4 []byte, ipv6 []byte) []byte {
	pkt := []byte{0x00, 0x00, 0x00, 0x00}

	appendTLV := func(tlvType uint16, value []byte) {
		hdr := make([]byte, 4)
		binary.BigEndian.PutUint16(hdr[0:2], tlvType)
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(value)))
		pkt = append(pkt, hdr...)
		pkt = append(pkt, value...)
	}

	if len(mac) == 6 {
		appendTLV(tlvMAC, mac)
	}
	if identity != "" {
		appendTLV(tlvIdentity, []byte(identity))
	}
	if version != "" {
		appendTLV(tlvVersion, []byte(version))
	}
	if platform != "" {
		appendTLV(tlvPlatform, []byte(platform))
	}
	if uptime != 0 {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uptime)
		appendTLV(tlvUptime, b)
	}
	if softwareID != "" {
		appendTLV(tlvSoftwareID, []byte(softwareID))
	}
	if board != "" {
		appendTLV(tlvBoard, []byte(board))
	}
	if len(ipv4) == 4 {
		appendTLV(tlvIPv4, ipv4)
	}
	if len(ipv6) == 16 {
		appendTLV(tlvIPv6, ipv6)
	}

	return pkt
}

func TestParsePacket_FullValidResponse(t *testing.T) {
	mac := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	ipv4 := []byte{192, 168, 1, 1}
	pkt := buildTestPacket(mac, "router.home", "7.16.2", "MikroTik", 3600, "some-id", "RB4011", ipv4, nil)

	dev, err := ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket() unexpected error = %v", err)
	}
	if dev.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddress = %q, want %q", dev.MACAddress, "aa:bb:cc:dd:ee:ff")
	}
	if dev.Identity != "router.home" {
		t.Errorf("Identity = %q, want %q", dev.Identity, "router.home")
	}
	if dev.Version != "7.16.2" {
		t.Errorf("Version = %q, want %q", dev.Version, "7.16.2")
	}
	if dev.Platform != "MikroTik" {
		t.Errorf("Platform = %q, want %q", dev.Platform, "MikroTik")
	}
	if dev.Uptime != 3600 {
		t.Errorf("Uptime = %d, want 3600", dev.Uptime)
	}
	if dev.SoftwareID != "some-id" {
		t.Errorf("SoftwareID = %q, want %q", dev.SoftwareID, "some-id")
	}
	if dev.Board != "RB4011" {
		t.Errorf("Board = %q, want %q", dev.Board, "RB4011")
	}
	if dev.IPv4Address != "192.168.1.1" {
		t.Errorf("IPv4Address = %q, want %q", dev.IPv4Address, "192.168.1.1")
	}
}

func TestParsePacket_IPv6OnlyAddress(t *testing.T) {
	mac := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	ipv6 := make([]byte, 16) // all-zeros IPv6 address
	ipv6[0] = 0xFE
	ipv6[1] = 0x80
	pkt := buildTestPacket(mac, "router.home", "", "", 0, "", "", nil, ipv6)

	dev, err := ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket() unexpected error = %v", err)
	}
	if dev.IPv4Address != "" {
		t.Errorf("IPv4Address = %q, want empty", dev.IPv4Address)
	}
	if dev.IPv6Address != "fe80::" {
		t.Errorf("IPv6Address = %q, want %q", dev.IPv6Address, "fe80::")
	}
}

func TestParsePacket_BothIPv4AndIPv6(t *testing.T) {
	mac := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	ipv4 := []byte{10, 0, 0, 1}
	ipv6 := make([]byte, 16)
	ipv6[0] = 0xFE
	ipv6[1] = 0x80

	// Manually construct a packet with both IPv4 and IPv6 TLVs
	pkt := []byte{0x00, 0x00, 0x00, 0x00}
	appendTLV := func(tlvType uint16, value []byte) {
		hdr := make([]byte, 4)
		binary.BigEndian.PutUint16(hdr[0:2], tlvType)
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(value)))
		pkt = append(pkt, hdr...)
		pkt = append(pkt, value...)
	}
	appendTLV(tlvMAC, mac)
	appendTLV(tlvIdentity, []byte("router.home"))
	// In MNDP, TLV type 17 contains IPv4 and TLV type 15 contains IPv6.
	appendTLV(tlvIPv4, ipv4) // IPv4: length 4 → should be stored
	appendTLV(tlvIPv6, ipv6) // IPv6: length 16 → should be parsed separately

	dev, err := ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket() unexpected error = %v", err)
	}
	if dev.IPv4Address != "10.0.0.1" {
		t.Errorf("IPv4Address = %q, want %q", dev.IPv4Address, "10.0.0.1")
	}
	if dev.IPv6Address != "fe80::" {
		t.Errorf("IPv6Address = %q, want %q", dev.IPv6Address, "fe80::")
	}
}

func TestParsePacket_TruncatedTLV(t *testing.T) {
	pkt := []byte{
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x01, // TLV type = 1 (MAC)
		0x00, 0x06, // TLV length = 6
		// Only 2 bytes of value instead of 6 — truncated
		0xAA, 0xBB,
	}

	_, err := ParsePacket(pkt)
	if err == nil {
		t.Fatal("ParsePacket() expected error for truncated TLV, got nil")
	}
}

func TestParsePacket_MissingMAC(t *testing.T) {
	// Packet with identity but no MAC
	pkt := []byte{
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x05, // TLV type = 5 (Identity)
		0x00, 0x06, // TLV length = 6
		'r', 'o', 'u', 't', 'e', 'r', // "router"
	}

	_, err := ParsePacket(pkt)
	if err == nil {
		t.Fatal("ParsePacket() expected error for missing MAC, got nil")
	}
}

func TestParsePacket_MissingIdentity(t *testing.T) {
	// Packet with MAC but no identity
	pkt := []byte{
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x01, // TLV type = 1 (MAC)
		0x00, 0x06, // TLV length = 6
		0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
	}

	_, err := ParsePacket(pkt)
	if err == nil {
		t.Fatal("ParsePacket() expected error for missing Identity, got nil")
	}
}

func TestParsePacket_TooShort(t *testing.T) {
	pkt := []byte{0x01, 0x00} // only 2 bytes

	_, err := ParsePacket(pkt)
	if err == nil {
		t.Fatal("ParsePacket() expected error for short packet, got nil")
	}
}

func TestParsePacket_RequestPacket(t *testing.T) {
	pkt := []byte{
		0x00, 0x00, // msg-type = 0x0000 (request)
		0x00, 0x00, // sequence
	}

	_, err := ParsePacket(pkt)
	if err == nil {
		t.Fatal("ParsePacket() expected error for request packet, got nil")
	}
}

func TestParsePacket_UnknownMsgType(t *testing.T) {
	pkt := []byte{
		0xFF, 0xFF, // msg-type = 0xFFFF (unknown)
		0x00, 0x00, // sequence
	}

	_, err := ParsePacket(pkt)
	if err == nil {
		t.Fatal("ParsePacket() expected error for unknown msg-type, got nil")
	}
}

func TestParsePacket_TLVEndiannessIsBigEndian(t *testing.T) {
	mac := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}

	t.Run("big-endian-tlv-header-parses", func(t *testing.T) {
		pkt := []byte{
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x01, // type: MAC
			0x00, 0x06, // len: 6
			0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
			0x00, 0x05, // type: Identity
			0x00, 0x06, // len: 6
			'r', 'o', 'u', 't', 'e', 'r',
		}

		dev, err := ParsePacket(pkt)
		if err != nil {
			t.Fatalf("ParsePacket() unexpected error for big-endian TLV header = %v", err)
		}
		if dev.MACAddress != "aa:bb:cc:dd:ee:ff" {
			t.Fatalf("MACAddress = %q, want %q", dev.MACAddress, "aa:bb:cc:dd:ee:ff")
		}
	})

	t.Run("little-endian-tlv-header-fails", func(t *testing.T) {
		pkt := []byte{
			0x00, 0x00, 0x00, 0x00,
			0x01, 0x00, // type: MAC (little-endian encoded)
			0x06, 0x00, // len: 6 (little-endian encoded)
		}
		pkt = append(pkt, mac...)
		pkt = append(pkt,
			0x05, 0x00, // type: Identity (little-endian encoded)
			0x06, 0x00, // len: 6 (little-endian encoded)
			'r', 'o', 'u', 't', 'e', 'r',
		)

		if _, err := ParsePacket(pkt); err == nil {
			t.Fatal("ParsePacket() expected error for little-endian TLV header, got nil")
		}
	})
}

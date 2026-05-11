package mndp

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	tlvMAC        = 1
	tlvIdentity   = 5
	tlvVersion    = 7
	tlvPlatform   = 8
	tlvUptime     = 10
	tlvSoftwareID = 11
	tlvBoard      = 12
	tlvIPv4       = 15

	msgTypeRequest  = 0x0000
	msgTypeResponse = 0x0001
)

// ParsePacket parses an MNDP response packet and returns the discovered Device.
//
// Wire format:
//
//	[2B msg-type LE][2B sequence LE] [TLV…]
//	TLV: [2B type LE][2B length LE][length bytes value]
//
// Returns an error for:
//   - Packets shorter than 4 bytes
//   - msg-type 0x0000 (request) or unknown types
//   - Truncated TLV values
//   - Missing MAC address TLV
//   - Missing Identity TLV
func ParsePacket(data []byte) (*Device, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("mndp: packet too short (%d bytes)", len(data))
	}

	msgType := binary.LittleEndian.Uint16(data[0:2])

	switch msgType {
	case msgTypeRequest:
		return nil, fmt.Errorf("mndp: packet is a request (msg-type 0x0000), not a response")
	case msgTypeResponse:
		// OK — continue parsing
	default:
		return nil, fmt.Errorf("mndp: unknown msg-type 0x%04x", msgType)
	}

	dev := &Device{}
	offset := 4

	for offset < len(data) {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("mndp: truncated TLV header at offset %d", offset)
		}
		tlvType := binary.LittleEndian.Uint16(data[offset : offset+2])
		tlvLen := int(binary.LittleEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4

		if offset+tlvLen > len(data) {
			return nil, fmt.Errorf("mndp: truncated TLV value: type=%d declared length=%d available=%d",
				tlvType, tlvLen, len(data)-offset)
		}

		value := data[offset : offset+tlvLen]
		offset += tlvLen

		switch tlvType {
		case tlvMAC:
			if tlvLen == 6 {
				dev.MACAddress = net.HardwareAddr(value).String()
			}
		case tlvIdentity:
			dev.Identity = string(value)
		case tlvVersion:
			dev.Version = string(value)
		case tlvPlatform:
			dev.Platform = string(value)
		case tlvUptime:
			if tlvLen == 4 {
				dev.Uptime = binary.LittleEndian.Uint32(value)
			}
		case tlvSoftwareID:
			dev.SoftwareID = string(value)
		case tlvBoard:
			dev.Board = string(value)
		case tlvIPv4:
			if tlvLen == 4 {
				dev.IPv4Address = net.IP(value).String()
			}
			// tlvLen == 16 is IPv6 — silently skipped (not yet supported)
		}
	}

	if dev.MACAddress == "" {
		return nil, fmt.Errorf("mndp: missing required MAC address TLV")
	}
	if dev.Identity == "" {
		return nil, fmt.Errorf("mndp: missing required Identity TLV")
	}

	return dev, nil
}

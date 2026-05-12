package mndp

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
)

const (
	tlvMAC                 = 1
	tlvIdentity            = 5
	tlvVersion             = 7
	tlvPlatform            = 8
	tlvUptime              = 10
	tlvSoftwareID          = 11
	tlvBoard               = 12
	tlvUnpack              = 14
	tlvIPv6                = 15
	tlvSourceInterfaceName = 16
	tlvIPv4                = 17
	tlvUnknown18           = 18 // observed in the wild; purpose unknown
)

// ParsePacket parses an MNDP packet and returns the discovered Device.
//
// Wire format:
//
//	Headers: [1B seq_lo or reserved] [1B msg-type or reserved]
//               [1B seq_hi or reserved] [1B counter or reserved]
//	Payload: [TLV…][2B type BE][2B length BE][length bytes value]
//
// Returns an error for:
//   - Packets shorter than 4 bytes
//   - Truncated TLV values
//   - Missing MAC address TLV
//   - Missing Identity TLV
func ParsePacket(data []byte) (*Device, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("mndp: packet too short (%d bytes)", len(data))
	}

	slog.Debug("mndp: packet first 4 bytes",
		"hex", fmt.Sprintf("% 02x", data[0:4]),
	)

	dev := &Device{}
	offset := 4

	for offset < len(data) {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("mndp: truncated TLV header at offset %d", offset)
		}
		tlvType := binary.BigEndian.Uint16(data[offset : offset+2])
		tlvLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
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
		case tlvUnpack:
			if tlvLen == 1 {
				dev.Unpack = value[0] != 0
			}
		case tlvIPv6:
			if tlvLen == 16 {
				dev.IPv6Address = net.IP(value).String()
			}
			// Other lengths silently skipped
		case tlvSourceInterfaceName:
			dev.SourceInterfaceName = string(value)
		case tlvIPv4:
			if tlvLen == 4 {
				dev.IPv4Address = net.IP(value).String()
			}
		case tlvUnknown18:
			// Observed but undocumented; silently skip
			slog.Debug("mndp: skipping unknown TLV 18",
				"type", tlvType,
				"length", tlvLen,
				"hex", fmt.Sprintf("% 02x", value),
			)
		default:
			slog.Debug("mndp: skipping unknown TLV",
				"type", tlvType,
				"length", tlvLen,
				"hex", fmt.Sprintf("% 02x", value),
			)
		}
	}

	slog.Debug("mndp: parsed device",
		"mac", dev.MACAddress,
		"identity", dev.Identity,
		"version", dev.Version,
		"platform", dev.Platform,
		"uptime", dev.Uptime,
		"softwareid", dev.SoftwareID,
		"board", dev.Board,
		"unpack", dev.Unpack,
		"ipv6", dev.IPv6Address,
		"remote_interface", dev.SourceInterfaceName,
		"ipv4", dev.IPv4Address,
		"local_interface", dev.InterfaceName,
	)

	if dev.MACAddress == "" {
		return nil, fmt.Errorf("mndp: missing required MAC address TLV")
	}
	if dev.Identity == "" {
		return nil, fmt.Errorf("mndp: missing required Identity TLV")
	}

	return dev, nil
}

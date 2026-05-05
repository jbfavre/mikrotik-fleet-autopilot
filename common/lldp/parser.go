package lldp

import (
	"fmt"
	"regexp"
	"strings"
)

// Globally define regexp to avoid compiling them on every call

// isRecordStart checks if a line begins with whitespace and a number
var isRecordStart = regexp.MustCompile(`^\s*\d+\s+`)

// extractRecordRemainder removes the leading number and whitespace from a record start line
var extractRecordRemainder = isRecordStart

// Checks key=value pattern
var checkKeyValue = regexp.MustCompile(`^\s+(\S+?)=`)

// DetectSourceIdentity extracts device name from prompt line like "[user@device] >"
var detectSourceIdentity = regexp.MustCompile(`\[.*?@(.+?)\]\s*[>/]`)

// normalizeWhitespace collapses multiple spaces and newlines into single spaces
var normalizeWhitespace = regexp.MustCompile(`\s+`)

// ParseNeighbors parses LLDP neighbor output into a structured ParseResult
func ParseNeighbors(raw string) (*ParseResult, error) {
	result := &ParseResult{
		Neighbors: make([]*Neighbor, 0),
		Warnings:  make([]string, 0),
	}

	if strings.TrimSpace(raw) == "" {
		return result, nil
	}

	// Extract source identity if present (from prompt line like "[user@device] >")
	result.SourceIdentity = ""
	matches := detectSourceIdentity.FindStringSubmatch(raw)
	if len(matches) > 1 {
		result.SourceIdentity = matches[1]
	}

	// Split into individual record strings
	records := splitRecords(raw)

	// Parse each record into a Neighbor
	for idx, recordStr := range records {
		fields, err := tokenizeKeyValue(recordStr)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("record %d: tokenization error: %v", idx, err))
			continue
		}

		neighbor, err := newNeighbor(idx, fields)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("record %d: %v", idx, err))
			continue
		}

		result.Neighbors = append(result.Neighbors, neighbor)
	}

	return result, nil
}

// splitRecords breaks raw output into numbered records
// Records start with a line matching /^\s*\d+\s+/
func splitRecords(raw string) []string {
	lines := strings.Split(raw, "\n")
	records := make([]string, 0)
	var current *strings.Builder

	for _, line := range lines {
		// Check if this line starts a new record (begins with number)
		if isRecordStart.MatchString(line) {
			// Save previous record if exists
			if current != nil {
				records = append(records, current.String())
			}
			// Start new record: extract number and remainder
			remainder := extractRecordRemainder.ReplaceAllString(line, "")
			current = &strings.Builder{}
			current.WriteString(remainder)
		} else if current != nil {
			// Continuation line: normalize leading whitespace and append
			trimmed := strings.TrimLeft(line, " \t")
			if trimmed != "" {
				current.WriteString(" ")
				current.WriteString(trimmed)
			}
		}
	}

	// Don't forget the last record
	if current != nil {
		records = append(records, current.String())
	}

	return records
}

// tokenizeKeyValue parses a single record string into key-value pairs
// Handles both quoted and unquoted values
func tokenizeKeyValue(record string) (map[string]string, error) {
	fields := make(map[string]string)

	// Normalize whitespace in multiline values (e.g., system-description)
	record = normalizeWhitespace.ReplaceAllString(record, " ")

	i := 0
	for i < len(record) {
		// Skip whitespace
		for i < len(record) && isSpace(record[i]) {
			i++
		}
		if i >= len(record) {
			break
		}

		// Find key (non-whitespace up to '=')
		keyStart := i
		for i < len(record) && record[i] != '=' && !isSpace(record[i]) {
			i++
		}
		if i >= len(record) || record[i] != '=' {
			// Not a valid key=value pair, skip to next whitespace
			for i < len(record) && !isSpace(record[i]) {
				i++
			}
			continue
		}

		key := record[keyStart:i]
		i++ // skip '='

		// Parse value
		if i < len(record) && record[i] == '"' {
			// Quoted value
			i++ // skip opening quote
			valStart := i
			for i < len(record) && record[i] != '"' {
				if record[i] == '\\' && i+1 < len(record) {
					i++ // skip escape char
				}
				i++
			}
			if i >= len(record) {
				return nil, fmt.Errorf("unterminated quoted value for key %q", key)
			}
			value := record[valStart:i]
			i++ // skip closing quote
			fields[key] = value
		} else {
			// Unquoted value: collect until next key= pattern or end
			valStart := i
			valEnd := i

			// Look ahead for next key= pattern
			for j := i; j < len(record); j++ {
				if isSpace(record[j]) {
					// Check if followed by a key=
					rest := record[j:]
					if match := checkKeyValue.FindStringIndex(rest); match != nil {
						valEnd = j
						break
					}
				}
				valEnd = j + 1
			}

			value := strings.TrimSpace(record[valStart:valEnd])
			fields[key] = value
			i = valEnd
		}
	}

	return fields, nil
}

// isSpace checks if character is whitespace
func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

// newNeighbor constructs a Neighbor from parsed fields
func newNeighbor(index int, fields map[string]string) (*Neighbor, error) {
	// Identity is required
	identity, ok := fields["identity"]
	if !ok || strings.TrimSpace(identity) == "" {
		return nil, fmt.Errorf("missing required field: identity")
	}

	n := &Neighbor{
		Index:                index,
		Identity:             identity,
		Platform:             getStringField(fields, "platform"),
		Version:              getStringField(fields, "version"),
		Board:                getStringField(fields, "board"),
		Address:              getStringField(fields, "address"),
		Address6:             getStringField(fields, "address6"),
		MacAddress:           getStringField(fields, "mac-address"),
		SystemDescription:    getStringField(fields, "system-description"),
		SystemCaps:           parseCommaSeparated(getStringField(fields, "system-caps")),
		SystemCapsEnabled:    parseCommaSeparated(getStringField(fields, "system-caps-enabled")),
		DiscoveredBy:         parseCommaSeparated(getStringField(fields, "discovered-by")),
		Age:                  getStringField(fields, "age"),
		Uptime:               getStringField(fields, "uptime"),
		SoftwareID:           getStringField(fields, "software-id"),
		Unpack:               getStringField(fields, "unpack"),
		IPv6Enabled:          getStringField(fields, "ipv6") == "yes",
		LocalInterfaceChain:  getStringField(fields, "interface"),
		RemoteInterfaceChain: getStringField(fields, "interface-name"),
	}

	// Extract physical interface from the chain
	n.LocalInterface = extractLocalInterface(n.LocalInterfaceChain)
	n.RemoteInterface = extractRemoteInterface(n.RemoteInterfaceChain)

	return n, nil
}

// getStringField safely retrieves a string field with default empty
func getStringField(fields map[string]string, key string) string {
	if v, ok := fields[key]; ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// parseCommaSeparated splits a comma-separated string into trimmed elements
func parseCommaSeparated(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// extractLocalInterface extracts the physical interface from a chain like "sfp-sfpplus3,br-lan"
// Returns the first component before the comma
func extractLocalInterface(chain string) string {
	if chain == "" {
		return ""
	}
	parts := strings.Split(chain, ",")
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}
	return strings.TrimSpace(chain)
}

// extractRemoteInterface extracts the physical interface from a chain like "br-lan/ether1"
// Returns the last component after the slash
func extractRemoteInterface(chain string) string {
	if chain == "" {
		return ""
	}
	parts := strings.Split(chain, "/")
	if len(parts) > 0 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return strings.TrimSpace(chain)
}

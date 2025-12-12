package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseHostsFlag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single host",
			input:    "192.168.1.1",
			expected: []string{"192.168.1.1"},
		},
		{
			name:     "multiple hosts with comma",
			input:    "192.168.1.1,192.168.1.2",
			expected: []string{"192.168.1.1", "192.168.1.2"},
		},
		{
			name:     "multiple hosts with spaces",
			input:    "192.168.1.1, 192.168.1.2, 192.168.1.3",
			expected: []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"},
		},
		{
			name:     "hosts with trailing comma",
			input:    "192.168.1.1,192.168.1.2,",
			expected: []string{"192.168.1.1", "192.168.1.2"},
		},
		{
			name:     "hosts with leading spaces",
			input:    " 192.168.1.1,  192.168.1.2",
			expected: []string{"192.168.1.1", "192.168.1.2"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "only spaces and commas",
			input:    " , , ",
			expected: []string{},
		},
		{
			name:     "hostname instead of IP",
			input:    "router.example.com",
			expected: []string{"router.example.com"},
		},
		{
			name:     "mixed hostnames and IPs",
			input:    "router1.local,192.168.1.1,router2.local",
			expected: []string{"router1.local", "192.168.1.1", "router2.local"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseHostsFlag(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseHostsFlag(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseHostsFlagPerformance(t *testing.T) {
	// Test with a large number of hosts
	largeInput := strings.Repeat("192.168.1.1,", 1000)
	result := parseHostsFlag(largeInput)

	if len(result) != 1000 {
		t.Errorf("Expected 1000 hosts, got %d", len(result))
	}
}

func BenchmarkParseHostsFlag(b *testing.B) {
	input := "192.168.1.1,192.168.1.2,192.168.1.3,router.local"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseHostsFlag(input)
	}
}

func BenchmarkParseHostsFlagLarge(b *testing.B) {
	// Benchmark with 100 hosts
	input := strings.Repeat("192.168.1.1,", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseHostsFlag(input)
	}
}

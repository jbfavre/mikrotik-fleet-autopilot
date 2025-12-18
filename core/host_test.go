package core

import (
	"testing"
)

func TestParseHost_IPAddress(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedType string
		expectedHost string
		expectedPort string
		expectedName string
	}{
		{
			name:         "IPv4 without port",
			input:        "192.168.1.1",
			expectedType: "ip",
			expectedHost: "192.168.1.1",
			expectedPort: "22",
			expectedName: "192.168.1.1",
		},
		{
			name:         "IPv4 with port",
			input:        "192.168.1.1:2222",
			expectedType: "ip",
			expectedHost: "192.168.1.1",
			expectedPort: "2222",
			expectedName: "192.168.1.1",
		},
		{
			name:         "IPv6 without port",
			input:        "::1",
			expectedType: "ip",
			expectedHost: "::1",
			expectedPort: "22",
			expectedName: "::1",
		},
		{
			name:         "IPv6 full address",
			input:        "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedType: "ip",
			expectedHost: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedPort: "22",
			expectedName: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseHost(tt.input)
			if info.Type != tt.expectedType {
				t.Errorf("ParseHost(%q).Type = %v, want %v", tt.input, info.Type, tt.expectedType)
			}
			if info.Hostname != tt.expectedHost {
				t.Errorf("ParseHost(%q).Hostname = %v, want %v", tt.input, info.Hostname, tt.expectedHost)
			}
			if info.Port != tt.expectedPort {
				t.Errorf("ParseHost(%q).Port = %v, want %v", tt.input, info.Port, tt.expectedPort)
			}
			if info.ShortName != tt.expectedName {
				t.Errorf("ParseHost(%q).ShortName = %v, want %v", tt.input, info.ShortName, tt.expectedName)
			}
			if info.Original != tt.input {
				t.Errorf("ParseHost(%q).Original = %v, want %v", tt.input, info.Original, tt.input)
			}
		})
	}
}

func TestParseHost_FQDN(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedType string
		expectedHost string
		expectedPort string
		expectedName string
	}{
		{
			name:         "Simple FQDN",
			input:        "router1.home.local",
			expectedType: "fqdn",
			expectedHost: "router1.home.local",
			expectedPort: "22",
			expectedName: "router1",
		},
		{
			name:         "FQDN with multiple domains",
			input:        "router1.example.com",
			expectedType: "fqdn",
			expectedHost: "router1.example.com",
			expectedPort: "22",
			expectedName: "router1",
		},
		{
			name:         "FQDN with port",
			input:        "router1.home.local:2222",
			expectedType: "fqdn",
			expectedHost: "router1.home.local",
			expectedPort: "2222",
			expectedName: "router1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseHost(tt.input)
			if info.Type != tt.expectedType {
				t.Errorf("ParseHost(%q).Type = %v, want %v", tt.input, info.Type, tt.expectedType)
			}
			if info.Hostname != tt.expectedHost {
				t.Errorf("ParseHost(%q).Hostname = %v, want %v", tt.input, info.Hostname, tt.expectedHost)
			}
			if info.Port != tt.expectedPort {
				t.Errorf("ParseHost(%q).Port = %v, want %v", tt.input, info.Port, tt.expectedPort)
			}
			if info.ShortName != tt.expectedName {
				t.Errorf("ParseHost(%q).ShortName = %v, want %v", tt.input, info.ShortName, tt.expectedName)
			}
		})
	}
}

func TestParseHost_Hostname(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedType string
		expectedHost string
		expectedPort string
		expectedName string
	}{
		{
			name:         "Simple hostname",
			input:        "router1",
			expectedType: "hostname",
			expectedHost: "router1",
			expectedPort: "22",
			expectedName: "router1",
		},
		{
			name:         "Hostname with port",
			input:        "router1:2222",
			expectedType: "hostname",
			expectedHost: "router1",
			expectedPort: "2222",
			expectedName: "router1",
		},
		{
			name:         "Hostname with numbers",
			input:        "router42",
			expectedType: "hostname",
			expectedHost: "router42",
			expectedPort: "22",
			expectedName: "router42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseHost(tt.input)
			if info.Type != tt.expectedType {
				t.Errorf("ParseHost(%q).Type = %v, want %v", tt.input, info.Type, tt.expectedType)
			}
			if info.Hostname != tt.expectedHost {
				t.Errorf("ParseHost(%q).Hostname = %v, want %v", tt.input, info.Hostname, tt.expectedHost)
			}
			if info.Port != tt.expectedPort {
				t.Errorf("ParseHost(%q).Port = %v, want %v", tt.input, info.Port, tt.expectedPort)
			}
			if info.ShortName != tt.expectedName {
				t.Errorf("ParseHost(%q).ShortName = %v, want %v", tt.input, info.ShortName, tt.expectedName)
			}
		})
	}
}

func TestIsIPAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"IPv4", "192.168.1.1", true},
		{"IPv4 localhost", "127.0.0.1", true},
		{"IPv6 short", "::1", true},
		{"IPv6 full", "2001:0db8:85a3:0000:0000:8a2e:0370:7334", true},
		{"Hostname", "router1", false},
		{"FQDN", "router1.home.local", false},
		{"Invalid IP", "999.999.999.999", false},
		{"Empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsIPAddress(tt.input)
			if result != tt.expected {
				t.Errorf("IsIPAddress(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func BenchmarkParseHost(b *testing.B) {
	testInputs := []string{
		"192.168.1.1",
		"router1.home.local",
		"router1",
		"192.168.1.1:2222",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range testInputs {
			ParseHost(input)
		}
	}
}

func BenchmarkIsIPAddress(b *testing.B) {
	testInputs := []string{
		"192.168.1.1",
		"router1.home.local",
		"router1",
		"::1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range testInputs {
			IsIPAddress(input)
		}
	}
}

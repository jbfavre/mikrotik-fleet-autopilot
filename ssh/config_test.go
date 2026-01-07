package ssh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigReader_ReadConfig(t *testing.T) {
	// Create temporary directory and override HOME to avoid reading real SSH config
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	reader := &DefaultConfigReader{}

	tests := []struct {
		name     string
		host     string
		wantType string
		wantHost string
		wantPort string
		wantName string
	}{
		{
			name:     "IPv4 without port",
			host:     "192.168.1.1",
			wantType: "ip",
			wantHost: "192.168.1.1",
			wantPort: "22",
			wantName: "192.168.1.1",
		},
		{
			name:     "IPv4 with port",
			host:     "192.168.1.1:2222",
			wantType: "ip",
			wantHost: "192.168.1.1",
			wantPort: "2222",
			wantName: "192.168.1.1",
		},
		{
			name:     "IPv6 without port",
			host:     "::1",
			wantType: "ip",
			wantHost: "::1",
			wantPort: "22",
			wantName: "::1",
		},
		{
			name:     "Simple FQDN",
			host:     "router1.home.local",
			wantType: "fqdn",
			wantHost: "router1.home.local",
			wantPort: "22",
			wantName: "router1",
		},
		{
			name:     "FQDN with port",
			host:     "router1.home.local:2222",
			wantType: "fqdn",
			wantHost: "router1.home.local",
			wantPort: "2222",
			wantName: "router1",
		},
		{
			name:     "Simple hostname",
			host:     "router1",
			wantType: "hostname",
			wantHost: "router1",
			wantPort: "22",
			wantName: "router1",
		},
		{
			name:     "Hostname with port",
			host:     "router42:2222",
			wantType: "hostname",
			wantHost: "router42",
			wantPort: "2222",
			wantName: "router42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := reader.ReadConfig(tt.host)
			if err != nil {
				t.Fatalf("ReadConfig(%q) unexpected error: %v", tt.host, err)
			}
			if info == nil {
				t.Fatalf("ReadConfig(%q) returned nil", tt.host)
			}
			if info.Type != tt.wantType {
				t.Errorf("ReadConfig(%q).Type = %v, want %v", tt.host, info.Type, tt.wantType)
			}
			if info.Hostname != tt.wantHost {
				t.Errorf("ReadConfig(%q).Hostname = %v, want %v", tt.host, info.Hostname, tt.wantHost)
			}
			if info.Port != tt.wantPort {
				t.Errorf("ReadConfig(%q).Port = %v, want %v", tt.host, info.Port, tt.wantPort)
			}
			if info.ShortName != tt.wantName {
				t.Errorf("ReadConfig(%q).ShortName = %v, want %v", tt.host, info.ShortName, tt.wantName)
			}
			if info.Original != tt.host {
				t.Errorf("ReadConfig(%q).Original = %v, want %v", tt.host, info.Original, tt.host)
			}
		})
	}
}

func TestDefaultConfigReader_WithSSHConfig(t *testing.T) {
	// Create temporary SSH config for testing
	tmpDir := t.TempDir()
	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("Failed to create temp .ssh dir: %v", err)
	}

	configContent := `
Host testrouter
    HostName 192.168.1.100
    User admin
    Port 2222
    IdentityFile ~/.ssh/test_key

Host *.local
    User localadmin
    Port 2200
`
	configPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Temporarily change HOME for this test
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	reader := &DefaultConfigReader{}

	tests := []struct {
		name     string
		host     string
		wantHost string
		wantUser string
		wantPort string
	}{
		{
			name:     "alias expansion",
			host:     "testrouter",
			wantHost: "192.168.1.100",
			wantUser: "admin",
			wantPort: "2222",
		},
		{
			name:     "wildcard match",
			host:     "router.local",
			wantHost: "router.local",
			wantUser: "localadmin",
			wantPort: "2200",
		},
		{
			name:     "no match - use defaults",
			host:     "unknown.host",
			wantHost: "unknown.host",
			wantUser: "",
			wantPort: "22",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostInfo, err := reader.ReadConfig(tt.host)
			if err != nil {
				t.Fatalf("ReadConfig() error = %v", err)
			}
			if hostInfo.Hostname != tt.wantHost {
				t.Errorf("ReadConfig().Hostname = %q, want %q", hostInfo.Hostname, tt.wantHost)
			}
			if hostInfo.User != tt.wantUser {
				t.Errorf("ReadConfig().User = %q, want %q", hostInfo.User, tt.wantUser)
			}
			if hostInfo.Port != tt.wantPort {
				t.Errorf("ReadConfig().Port = %q, want %q", hostInfo.Port, tt.wantPort)
			}
		})
	}
}

// TestParseHost_IPAddress tests ParseHost with IP address inputs
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

// TestParseHost_FQDN tests ParseHost with FQDN inputs
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

// TestParseHost_Hostname tests ParseHost with simple hostname inputs
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

// TestIsIPAddress tests the IsIPAddress function
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

// BenchmarkParseHost benchmarks the ParseHost function
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

// BenchmarkIsIPAddress benchmarks the IsIPAddress function
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

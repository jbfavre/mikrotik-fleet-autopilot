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

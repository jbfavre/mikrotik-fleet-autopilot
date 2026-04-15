package core

import (
	"runtime"
	"testing"
)

func TestConfig_GetSkipHostKeyCheck(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		want   bool
	}{
		{
			name: "skip host key check enabled",
			config: &Config{
				Hosts:            []string{"192.168.1.1"},
				User:             "admin",
				Debug:            false,
				SkipHostKeyCheck: true,
			},
			want: true,
		},
		{
			name: "skip host key check disabled",
			config: &Config{
				Hosts:            []string{"192.168.1.1"},
				User:             "admin",
				Debug:            false,
				SkipHostKeyCheck: false,
			},
			want: false,
		},
		{
			name: "default config (skip disabled)",
			config: &Config{
				Hosts: []string{"192.168.1.1"},
				User:  "admin",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetSkipHostKeyCheck(); got != tt.want {
				t.Errorf("Config.GetSkipHostKeyCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveMaxConcurrentHosts(t *testing.T) {
	tests := []struct {
		name            string
		configuredValue int
		want            int
		wantErr         bool
	}{
		{
			name:            "zero auto-detects cpu count",
			configuredValue: 0,
			want:            runtime.NumCPU(),
		},
		{
			name:            "one forces sequential",
			configuredValue: 1,
			want:            1,
		},
		{
			name:            "explicit parallel cap accepted",
			configuredValue: 6,
			want:            6,
		},
		{
			name:            "negative value rejected",
			configuredValue: -1,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveMaxConcurrentHosts(tt.configuredValue)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveMaxConcurrentHosts() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ResolveMaxConcurrentHosts() = %d, want %d", got, tt.want)
			}
		})
	}
}

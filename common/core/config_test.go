package core

import "testing"

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

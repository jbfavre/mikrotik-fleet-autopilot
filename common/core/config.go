package core

import (
	"fmt"
	"runtime"
)

type Config struct {
	Hosts                  []string
	User                   string
	Debug                  bool
	SkipHostKeyCheck       bool
	MaxConcurrentHosts     int
	EffectiveMaxConcurrent int
	DisplayMode            string
}

// ResolveMaxConcurrentHosts returns the effective host concurrency cap.
//
// Behavior:
//   - negative values are rejected with an error
//   - 0 auto-detects using GOMAXPROCS (respects container CPU quotas and GOMAXPROCS env var)
//   - 1 forces sequential execution
//   - 2 and above enables parallel processing with that cap
func ResolveMaxConcurrentHosts(configuredValue int) (int, error) {
	if configuredValue < 0 {
		return 0, fmt.Errorf("--max-concurrent-hosts must be 0 (auto-detect), 1 (sequential), or >= 2")
	}
	if configuredValue == 0 {
		return runtime.GOMAXPROCS(0), nil
	}
	return configuredValue, nil
}

// GetSkipHostKeyCheck returns the SkipHostKeyCheck flag for host key verification
func (c *Config) GetSkipHostKeyCheck() bool {
	return c.SkipHostKeyCheck
}

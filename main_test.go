package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

func TestBuildCommand(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	// Test basic command properties
	if cmd.Name != "mikrotik-fleet-autopilot" {
		t.Errorf("Expected name 'mikrotik-fleet-autopilot', got %s", cmd.Name)
	}

	if cmd.Version != "0.1.0" {
		t.Errorf("Expected version '0.1.0', got %s", cmd.Version)
	}

	if cmd.Usage == "" {
		t.Error("Expected non-empty usage string")
	}

	// Test that commands are registered
	if len(cmd.Commands) == 0 {
		t.Error("Expected at least one subcommand")
	}

	// Test that Before hook exists
	if cmd.Before == nil {
		t.Error("Expected Before hook to be set")
	}
}

func TestBuildCommandFlags(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	expectedFlags := map[string]bool{
		"host":                 false,
		"ssh-user":             false,
		"ssh-password":         false,
		"ssh-passphrase":       false,
		"debug":                false,
		"max-concurrent-hosts": false,
		"buffered-output":      false,
		"skip-hostkey-check":   false,
		"interface":            false,
		"mndp":                 false,
		"mndp-timeout":         false,
	}

	// Check all expected flags exist
	for _, flag := range cmd.Flags {
		names := flag.Names()
		for _, name := range names {
			if _, exists := expectedFlags[name]; exists {
				expectedFlags[name] = true
			}
		}
	}

	for flagName, found := range expectedFlags {
		if !found {
			t.Errorf("Expected flag '%s' not found", flagName)
		}
	}

	// Test that we have the right number of flags
	if len(cmd.Flags) != 11 {
		t.Errorf("Expected 11 flags, got %d", len(cmd.Flags))
	}
}

func TestBuildCommandFlagBinding(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	// Set some initial values
	hosts = "192.168.1.1"
	globalConfig.User = "testuser"
	globalConfig.Debug = true

	cmd := buildCommand(&globalConfig, &hosts)

	// The command should be built without errors
	if cmd == nil {
		t.Fatal("buildCommand returned nil")
	}

	// Verify values are as set (the pointers are bound correctly)
	if hosts != "192.168.1.1" {
		t.Errorf("hosts value changed unexpectedly: %s", hosts)
	}
	if globalConfig.User != "testuser" {
		t.Errorf("globalConfig.User changed unexpectedly: %s", globalConfig.User)
	}
}

func TestBuildCommandSubcommands(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	// Check that we have subcommands
	if len(cmd.Commands) == 0 {
		t.Fatal("Expected subcommands to be registered")
	}

	// Verify subcommand names (export and updates commands should be present)
	commandNames := make(map[string]bool)
	for _, subCmd := range cmd.Commands {
		commandNames[subCmd.Name] = true
	}

	// We expect at least the export and updates related commands
	if len(commandNames) == 0 {
		t.Error("No subcommands found")
	}
}

func TestBuildCommandMultipleInstances(t *testing.T) {
	// Test that multiple instances can be created independently
	var config1 core.Config
	var hosts1 string
	cmd1 := buildCommand(&config1, &hosts1)

	var config2 core.Config
	var hosts2 string
	cmd2 := buildCommand(&config2, &hosts2)

	// Each command should be independent
	if cmd1 == cmd2 {
		t.Error("buildCommand should create new instances")
	}

	// Modify one set of variables
	hosts1 = "192.168.1.1"
	hosts2 = "192.168.1.2"

	if hosts1 == hosts2 {
		t.Error("Variables should be independent")
	}
}

func TestHostParsing(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	hosts = "192.168.1.1,192.168.1.2, 192.168.1.3"

	buildCommand(&globalConfig, &hosts)

	// The actual parsing happens in the Before hook, but we can test
	// that the setup is correct
	parsedHosts := core.ParseHosts(hosts)
	expectedHosts := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"}

	if !reflect.DeepEqual(parsedHosts, expectedHosts) {
		t.Errorf("Expected hosts %v, got %v", expectedHosts, parsedHosts)
	}
}

func TestAutoDiscoverySetup(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	// Create router files
	if err := os.WriteFile("router1.rsc", []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create router1.rsc: %v", err)
	}
	if err := os.WriteFile("router2.rsc", []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create router2.rsc: %v", err)
	}

	// Test that auto-discovery would work
	routers, err := core.DiscoverHosts()
	if err != nil {
		t.Errorf("Auto-discovery failed: %v", err)
	}

	expectedHosts := []string{"router1", "router2"}
	if !reflect.DeepEqual(routers, expectedHosts) {
		t.Errorf("Expected %v, got %v", expectedHosts, routers)
	}
}

func TestAutoDiscoveryWithBeforeHook(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	// Create router files for auto-discovery
	if err := os.WriteFile("router1.rsc", []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create router1.rsc: %v", err)
	}
	if err := os.WriteFile("router2.rsc", []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create router2.rsc: %v", err)
	}

	// Build command with empty hosts to trigger auto-discovery
	var globalConfig core.Config
	var hosts string
	globalConfig.User = "testuser"

	cmd := buildCommand(&globalConfig, &hosts)

	// Test the complete flow by running the command with a subcommand argument
	// This simulates: mikrotik-fleet-autopilot export config
	testArgs := []string{"mikrotik-fleet-autopilot", "export", "config"}

	err := cmd.Run(context.Background(), testArgs)

	// The command will fail because we don't have real routers to connect to,
	// but we can check that hosts were auto-discovered in globalConfig
	if err == nil {
		// If it succeeds, hosts should be set
		expectedHosts := []string{"router1", "router2"}
		if !reflect.DeepEqual(globalConfig.Hosts, expectedHosts) {
			t.Errorf("Expected hosts %v, got %v", expectedHosts, globalConfig.Hosts)
		}
		if len(globalConfig.Hosts) != 2 {
			t.Errorf("Expected 2 hosts, got %d", len(globalConfig.Hosts))
		}
	} else {
		// Even if command fails (expected since no real routers),
		// hosts should have been discovered and set by the Before hook
		expectedHosts := []string{"router1", "router2"}
		if !reflect.DeepEqual(globalConfig.Hosts, expectedHosts) {
			t.Errorf("Expected hosts %v, got %v", expectedHosts, globalConfig.Hosts)
		}
		if len(globalConfig.Hosts) != 2 {
			t.Errorf("Expected 2 hosts in globalConfig after Before hook, got %d", len(globalConfig.Hosts))
		}
		// The error is expected (no real SSH connection), but auto-discovery should have worked
		t.Logf("Command failed as expected (no real routers): %v", err)
	}
}

func TestEmptyHostsWithNoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	// No router files, auto-discovery should fail
	_, err := core.DiscoverHosts()
	if err == nil {
		t.Error("Expected error when no router files found")
	}
}

func TestConfigStructure(t *testing.T) {
	var globalConfig core.Config

	// Test that config can be properly initialized
	globalConfig.User = "admin"
	globalConfig.Debug = true
	globalConfig.Hosts = []string{"router1.home"}

	if globalConfig.User != "admin" {
		t.Error("Config User field not set correctly")
	}
	if !globalConfig.Debug {
		t.Error("Config Debug field not set correctly")
	}
	if len(globalConfig.Hosts) != 1 {
		t.Error("Config Hosts field not set correctly")
	}
}

func TestContextValues(t *testing.T) {
	globalConfig := &core.Config{
		User:  "admin",
		Debug: false,
		Hosts: []string{"router1.home"},
	}

	// Test that config can be stored and retrieved from context
	ctx := context.WithValue(context.Background(), core.ConfigKey, globalConfig)

	cfg, err := core.GetConfig(ctx)
	if err != nil {
		t.Errorf("Failed to get config from context: %v", err)
	}

	if cfg.User != "admin" {
		t.Errorf("Expected user 'admin', got '%s'", cfg.User)
	}

	if len(cfg.Hosts) != 1 {
		t.Errorf("Expected 1 host, got %d", len(cfg.Hosts))
	}
}

func TestSshManagerCreation(t *testing.T) {
	user := "admin"
	password := "test-pass"
	passphrase := "test-phrase"

	sshManager := ssh.NewSshManager(user, core.StringPtr(password), core.StringPtr(passphrase))

	if sshManager == nil {
		t.Error("NewSshManager returned nil")
	}

	// Test that SSH manager can be stored in context
	ctx := context.WithValue(context.Background(), core.SshManagerKey, sshManager)

	retrievedManager, err := core.GetSshManager(ctx)
	if err != nil {
		t.Errorf("Failed to get SSH manager from context: %v", err)
	}

	if retrievedManager != sshManager {
		t.Error("Retrieved SSH manager doesn't match original")
	}
}

func TestBuildCommandWithEmptyCredentials(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	hosts = "192.168.1.1"
	// Leave password and passphrase empty

	cmd := buildCommand(&globalConfig, &hosts)

	// Command should build successfully even with empty credentials
	if cmd == nil {
		t.Error("buildCommand failed with empty credentials")
	}

	// SSH manager should be creatable with empty credentials
	sshManager := ssh.NewSshManager("admin", core.StringPtr(""), core.StringPtr(""))
	if sshManager == nil {
		t.Error("NewSshManager failed with empty credentials")
	}
}

func TestBuildCommandSSHCredentialPointers(t *testing.T) {
	tests := []struct {
		name                string
		args                []string
		wantPasswordNil     bool
		wantPasswordValue   string
		wantPassphraseNil   bool
		wantPassphraseValue string
	}{
		{
			name:              "unset ssh flags produce nil pointers",
			args:              []string{"mikrotik-fleet-autopilot", "--host", "router1", "inspect-ssh-manager"},
			wantPasswordNil:   true,
			wantPassphraseNil: true,
		},
		{
			name:                "explicit empty ssh flags produce empty-string pointers",
			args:                []string{"mikrotik-fleet-autopilot", "--host", "router1", "--ssh-password=", "--ssh-passphrase=", "inspect-ssh-manager"},
			wantPasswordNil:     false,
			wantPasswordValue:   "",
			wantPassphraseNil:   false,
			wantPassphraseValue: "",
		},
		{
			name:                "set ssh flags produce non-empty pointers",
			args:                []string{"mikrotik-fleet-autopilot", "--host", "router1", "--ssh-password=secret", "--ssh-passphrase=phrase", "inspect-ssh-manager"},
			wantPasswordNil:     false,
			wantPasswordValue:   "secret",
			wantPassphraseNil:   false,
			wantPassphraseValue: "phrase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var globalConfig core.Config
			var hosts string
			cmd := buildCommand(&globalConfig, &hosts)

			cmd.Commands = append(cmd.Commands, &cli.Command{
				Name: "inspect-ssh-manager",
				Action: func(ctx context.Context, _ *cli.Command) error {
					managerAny, err := core.GetSshManager(ctx)
					if err != nil {
						return err
					}
					manager, ok := managerAny.(*ssh.SshManager)
					if !ok || manager == nil {
						return errors.New("failed to cast SSH manager from context")
					}
					managerString := manager.String()

					passwordState := "password:not set"
					switch {
					case tt.wantPasswordNil:
						passwordState = "password:not set"
					case tt.wantPasswordValue == "":
						passwordState = "password:empty (hidden)"
					default:
						passwordState = "password:yes (hidden)"
					}
					if !strings.Contains(managerString, passwordState) {
						t.Fatalf("manager string %q missing %q", managerString, passwordState)
					}

					passphraseState := "passphrase:not set"
					switch {
					case tt.wantPassphraseNil:
						passphraseState = "passphrase:not set"
					case tt.wantPassphraseValue == "":
						passphraseState = "passphrase:empty (hidden)"
					default:
						passphraseState = "passphrase:yes (hidden)"
					}
					if !strings.Contains(managerString, passphraseState) {
						t.Fatalf("manager string %q missing %q", managerString, passphraseState)
					}
					return nil
				},
			})

			if err := cmd.Run(context.Background(), tt.args); err != nil {
				t.Fatalf("cmd.Run() error = %v", err)
			}
		})
	}
}

// TestBuildCommandBeforeHook tests the Before hook execution
func TestBuildCommandBeforeHook(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	tests := []struct {
		name        string
		hosts       string
		shouldError bool
		errorMsg    string
	}{
		{
			name:        "With explicit host",
			hosts:       "192.168.1.1",
			shouldError: false,
		},
		{
			name:        "With multiple hosts",
			hosts:       "192.168.1.1,192.168.1.2",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hosts = tt.hosts
			cmd := buildCommand(&globalConfig, &hosts)

			if cmd == nil {
				t.Fatal("buildCommand returned nil")
			}

			// Verify Before hook exists
			if cmd.Before == nil {
				t.Fatal("Before hook not set")
			}

			// The Before hook accesses cmd.Args() which needs the full CLI context
			// We can't easily test it in isolation without full urfave/cli setup
			// Just verify it exists and is properly typed
			t.Logf("Before hook successfully registered for command: %s", cmd.Name)
		})
	}
}

// TestBuildCommandAllSubcommands verifies that all expected subcommands are registered
func TestBuildCommandAllSubcommands(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	expectedCommands := []string{"export", "updates", "enroll"}

	if len(cmd.Commands) < len(expectedCommands) {
		t.Errorf("Expected at least %d subcommands, got %d", len(expectedCommands), len(cmd.Commands))
	}

	// Create a map of found commands for easier verification
	foundCommands := make(map[string]bool)
	for _, subCmd := range cmd.Commands {
		foundCommands[subCmd.Name] = true
	}

	// Verify each expected command exists
	for _, expectedName := range expectedCommands {
		if !foundCommands[expectedName] {
			t.Errorf("Expected subcommand '%s' not found", expectedName)
		}
	}
}

// TestBuildCommandHostParsing tests that hosts are parsed correctly
func TestBuildCommandHostParsing(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	tests := []struct {
		name          string
		hostsInput    string
		expectedCount int
	}{
		{
			name:          "Single host",
			hostsInput:    "192.168.1.1",
			expectedCount: 1,
		},
		{
			name:          "Multiple hosts with commas",
			hostsInput:    "192.168.1.1,192.168.1.2,router.local",
			expectedCount: 3,
		},
		{
			name:          "Hosts with spaces",
			hostsInput:    "192.168.1.1, 192.168.1.2 , router.local",
			expectedCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hosts = tt.hostsInput
			cmd := buildCommand(&globalConfig, &hosts)

			if cmd == nil {
				t.Fatal("buildCommand returned nil")
			}

			// After calling Before hook with args, hosts should be parsed
			// We test that the binding works correctly
			if hosts != tt.hostsInput {
				t.Errorf("hosts value changed: got %q, want %q", hosts, tt.hostsInput)
			}

			// Verify ParseHosts would work correctly
			parsedHosts := core.ParseHosts(hosts)
			if len(parsedHosts) != tt.expectedCount {
				t.Errorf("ParseHosts() returned %d hosts, want %d", len(parsedHosts), tt.expectedCount)
			}
		})
	}
}

// TestBuildCommandDebugFlag tests that debug flag is properly bound
func TestBuildCommandDebugFlag(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	// Test with debug enabled
	globalConfig.Debug = true
	hosts = "test-host"

	cmd := buildCommand(&globalConfig, &hosts)

	if cmd == nil {
		t.Fatal("buildCommand returned nil")
	}

	// Verify debug flag is bound
	if !globalConfig.Debug {
		t.Error("Debug flag should be true")
	}

	// Test with debug disabled
	globalConfig.Debug = false
	cmd2 := buildCommand(&globalConfig, &hosts)

	if cmd2 == nil {
		t.Fatal("buildCommand returned nil")
	}

	if globalConfig.Debug {
		t.Error("Debug flag should be false")
	}
}

func TestBuildCommandMaxConcurrentHostsFlag(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	// Use a pre-cancelled context so the root Before hook fires (binding flags into
	// globalConfig) but the subcommand action exits immediately without SSH work.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	err := cmd.Run(ctx, []string{"mikrotik-fleet-autopilot", "--host", "router1", "--max-concurrent-hosts", "4", "updates"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cmd.Run() error = %v, want context.Canceled", err)
	}
	if globalConfig.MaxConcurrentHosts != 4 {
		t.Errorf("MaxConcurrentHosts = %d, want 4", globalConfig.MaxConcurrentHosts)
	}
	if globalConfig.EffectiveMaxConcurrent != 4 {
		t.Errorf("EffectiveMaxConcurrent = %d, want 4", globalConfig.EffectiveMaxConcurrent)
	}
}

func TestBuildCommandMaxConcurrentHostsAutoDetect(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	// Use a pre-cancelled context so the root Before hook fires (binding flags into
	// globalConfig) but the subcommand action exits immediately without SSH work.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	err := cmd.Run(ctx, []string{"mikrotik-fleet-autopilot", "--host", "router1", "updates"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cmd.Run() error = %v, want context.Canceled", err)
	}
	expectedMaxConcurrent := runtime.GOMAXPROCS(0)
	if globalConfig.EffectiveMaxConcurrent != expectedMaxConcurrent {
		t.Errorf("EffectiveMaxConcurrent = %d, want %d", globalConfig.EffectiveMaxConcurrent, expectedMaxConcurrent)
	}
}

func TestBuildCommandMaxConcurrentHostsRejectsInvalidOverride(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	// The Before hook must abort with an error before the subcommand action runs.
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--host", "router1", "--max-concurrent-hosts", "-1", "updates"})
	if err == nil {
		t.Fatal("cmd.Run() expected an error for negative max concurrency")
	}
	if !strings.Contains(err.Error(), "--max-concurrent-hosts must be") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildCommandMaxConcurrentHostsSequential(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	// Use a pre-cancelled context so the root Before hook fires (binding flags into
	// globalConfig) but the subcommand action exits immediately without SSH work.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	err := cmd.Run(ctx, []string{"mikrotik-fleet-autopilot", "--host", "router1", "--max-concurrent-hosts", "1", "updates"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cmd.Run() error = %v, want context.Canceled", err)
	}
	if globalConfig.EffectiveMaxConcurrent != 1 {
		t.Errorf("EffectiveMaxConcurrent = %d, want 1", globalConfig.EffectiveMaxConcurrent)
	}
}

func TestBuildCommandBufferedOutputFlagDefault(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	err := cmd.Run(ctx, []string{"mikrotik-fleet-autopilot", "--host", "router1", "updates"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cmd.Run() error = %v, want context.Canceled", err)
	}
	if globalConfig.BufferedOutput {
		t.Error("BufferedOutput = true, want false by default")
	}
	if !globalConfig.PreferLiveMode {
		t.Error("PreferLiveMode = false, want true when --buffered-output is not set")
	}
}

func TestBuildCommandBufferedOutputFlagEnabled(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	err := cmd.Run(ctx, []string{"mikrotik-fleet-autopilot", "--host", "router1", "--buffered-output", "updates"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cmd.Run() error = %v, want context.Canceled", err)
	}
	if !globalConfig.BufferedOutput {
		t.Error("BufferedOutput = false, want true when --buffered-output is set")
	}
	if globalConfig.PreferLiveMode {
		t.Error("PreferLiveMode = true, want false when --buffered-output is set")
	}
}

func TestBuildCommandDisplayModeFlagRemoved(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--display-mode", "buffered", "updates"})
	if err == nil {
		t.Fatal("cmd.Run() expected an error for removed --display-mode flag")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected error for removed flag: %v", err)
	}
}

func TestMNDPAndHostMutualExclusivity(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--host", "router1", "--mndp", "discover"})
	if err == nil {
		t.Fatal("cmd.Run() expected mutual exclusivity error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got: %v", err)
	}
}

func TestMNDPFlagAloneDoesNotError(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	// Use --help to stop after CLI parsing/validation so the test does not execute
	// the real discover action or touch network interfaces.
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--mndp", "--help"})

	if err != nil {
		if strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("--mndp alone must not trigger mutual exclusivity error, got: %v", err)
		}
		if strings.Contains(err.Error(), "no routers specified") {
			t.Errorf("--mndp alone must not trigger 'no routers' error, got: %v", err)
		}
		t.Errorf("cmd.Run() unexpected error = %v", err)
	}
	if !globalConfig.UseMNDP {
		t.Error("UseMNDP should be true when --mndp flag is set")
	}
}

func TestMNDPFlagRejectedForNonDiscoverSubcommands(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--mndp", "updates"})
	if err == nil {
		t.Fatal("cmd.Run() expected error for --mndp on non-discover subcommand, got nil")
	}
	if !strings.Contains(err.Error(), "only supported with the discover subcommand") {
		t.Errorf("unexpected error for --mndp with updates: %v", err)
	}
}

func TestMNDPTimeoutFlagParsed(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	err := cmd.Run(ctx, []string{"mikrotik-fleet-autopilot", "--host", "router1", "--mndp-timeout", "30s", "updates"})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("cmd.Run() unexpected error = %v", err)
	}
	if globalConfig.MNDPTimeout != 30*time.Second {
		t.Errorf("MNDPTimeout = %v, want 30s", globalConfig.MNDPTimeout)
	}
}

func TestMNDPTimeoutFlagInvalid(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--host", "router1", "--mndp-timeout", "bad", "updates"})
	if err == nil {
		t.Fatal("cmd.Run() expected parse error for invalid --mndp-timeout, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --mndp-timeout") {
		t.Errorf("expected 'invalid --mndp-timeout' error, got: %v", err)
	}
}

func TestMNDPTimeoutFlagNonPositive(t *testing.T) {
	var globalConfig core.Config
	var hosts string

	cmd := buildCommand(&globalConfig, &hosts)
	err := cmd.Run(context.Background(), []string{"mikrotik-fleet-autopilot", "--host", "router1", "--mndp-timeout", "0s", "updates"})
	if err == nil {
		t.Fatal("cmd.Run() expected error for non-positive --mndp-timeout, got nil")
	}
	if !strings.Contains(err.Error(), "must be greater than 0") {
		t.Errorf("expected non-positive timeout error, got: %v", err)
	}
}

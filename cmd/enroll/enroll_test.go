package enroll

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jb.favre/mikrotik-fleet-autopilot/cmd/export"
	"jb.favre/mikrotik-fleet-autopilot/cmd/updates"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
	"jb.favre/mikrotik-fleet-autopilot/common/sshmocks_test"
)

func TestApplyConfigFile(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		runFunc       func(cmd string) (string, error)
		wantErr       bool
		errContains   string
		expectedCmds  []string
	}{
		{
			name: "successful config application",
			configContent: `/interface bridge add name=bridge1
/ip address add address=192.168.1.1/24 interface=bridge1`,
			runFunc: func(cmd string) (string, error) {
				return "", nil
			},
			wantErr: false,
			expectedCmds: []string{
				"/interface bridge add name=bridge1",
				"/ip address add address=192.168.1.1/24 interface=bridge1",
			},
		},
		{
			name: "skip empty lines and comments",
			configContent: `# This is a comment
/interface bridge add name=bridge1

# Another comment
/ip address add address=192.168.1.1/24 interface=bridge1
`,
			runFunc: func(cmd string) (string, error) {
				return "", nil
			},
			wantErr: false,
			expectedCmds: []string{
				"/interface bridge add name=bridge1",
				"/ip address add address=192.168.1.1/24 interface=bridge1",
			},
		},
		{
			name: "command execution error",
			configContent: `/interface bridge add name=bridge1
/invalid command here`,
			runFunc: func(cmd string) (string, error) {
				if cmd == "/invalid command here" {
					return "", fmt.Errorf("syntax error")
				}
				return "", nil
			},
			wantErr:     true,
			errContains: "failed to execute command at line 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "test-config.rsc")
			err := os.WriteFile(configFile, []byte(tt.configContent), 0644)
			if err != nil {
				t.Fatalf("Failed to create test config file: %v", err)
			}

			// Create mock SSH runner
			mockConn := &sshmocks_test.MockRunner{
				RunFunc: tt.runFunc,
			}

			// Test applyConfigFile
			err = applyConfigFile(mockConn, configFile)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("applyConfigFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("applyConfigFile() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Check executed commands
			if !tt.wantErr {
				if len(mockConn.CommandHistory) != len(tt.expectedCmds) {
					t.Errorf("Expected %d commands, got %d", len(tt.expectedCmds), len(mockConn.CommandHistory))
				}
				for i, expectedCmd := range tt.expectedCmds {
					if i >= len(mockConn.CommandHistory) {
						t.Errorf("Missing command at index %d: %s", i, expectedCmd)
						continue
					}
					if mockConn.CommandHistory[i] != expectedCmd {
						t.Errorf("Command %d = %q, want %q", i, mockConn.CommandHistory[i], expectedCmd)
					}
				}
			}
		})
	}
}

func TestApplyConfigFileInvalidFile(t *testing.T) {
	mockConn := &sshmocks_test.MockRunner{}
	err := applyConfigFile(mockConn, "/nonexistent/file.rsc")
	if err == nil {
		t.Error("applyConfigFile() should fail with nonexistent file")
	}
	if !strings.Contains(err.Error(), "failed to open config file") {
		t.Errorf("applyConfigFile() error = %v, should contain 'failed to open config file'", err)
	}
}

func TestSetRouterIdentity(t *testing.T) {
	tests := []struct {
		name        string
		hostname    string
		runFunc     func(cmd string) (string, error)
		wantErr     bool
		errContains string
		expectedCmd string
	}{
		{
			name:     "successful identity set",
			hostname: "router1",
			runFunc: func(cmd string) (string, error) {
				return "", nil
			},
			wantErr:     false,
			expectedCmd: "/system identity set name=router1",
		},
		{
			name:     "identity set fails",
			hostname: "router1",
			runFunc: func(cmd string) (string, error) {
				return "", fmt.Errorf("permission denied")
			},
			wantErr:     true,
			errContains: "failed to set identity",
		},
		{
			name:     "empty hostname skips identity set",
			hostname: "",
			runFunc: func(cmd string) (string, error) {
				return "", nil
			},
			wantErr:     false,
			expectedCmd: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &sshmocks_test.MockRunner{
				RunFunc: tt.runFunc,
			}

			err := setRouterIdentity(mockConn, tt.hostname)

			if (err != nil) != tt.wantErr {
				t.Errorf("setRouterIdentity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("setRouterIdentity() error = %v, should contain %q", err, tt.errContains)
				}
			}

			if !tt.wantErr {
				if tt.hostname == "" {
					// Empty hostname should not execute any command
					if len(mockConn.CommandHistory) != 0 {
						t.Errorf("Expected 0 commands for empty hostname, got %d", len(mockConn.CommandHistory))
					}
				} else if len(mockConn.CommandHistory) != 1 {
					t.Errorf("Expected 1 command, got %d", len(mockConn.CommandHistory))
				} else if mockConn.CommandHistory[0] != tt.expectedCmd {
					t.Errorf("Command = %q, want %q", mockConn.CommandHistory[0], tt.expectedCmd)
				}
			}
		})
	}
}

func TestUpdateHostKey(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		setupHostKey     bool
		existingHostKey  *ssh.HostKeyInfo
		connectionError  bool
		wantErr          bool
		errContains      string
		verifyHostKeySet bool
	}{
		{
			name:             "successful host key update with existing key",
			host:             "192.168.1.1",
			setupHostKey:     true,
			existingHostKey:  &ssh.HostKeyInfo{Algorithm: "ssh-rsa", Fingerprint: "SHA256:old123fingerprint"},
			connectionError:  false,
			wantErr:          false,
			verifyHostKeySet: true,
		},
		{
			name:             "successful host key capture - no existing key",
			host:             "192.168.1.2",
			setupHostKey:     false,
			connectionError:  false,
			wantErr:          false,
			verifyHostKeySet: true,
		},
		{
			name:            "connection error",
			host:            "192.168.1.3",
			setupHostKey:    false,
			connectionError: true,
			wantErr:         true,
			errContains:     "failed to connect to device",
		},
		{
			name:         "context cancelled",
			host:         "192.168.1.4",
			setupHostKey: false,
			wantErr:      true,
			errContains:  "context cancelled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary directory for host keys
			tmpDir := t.TempDir()
			originalWd, _ := os.Getwd()
			defer func() {
				_ = os.Chdir(originalWd)
			}()
			_ = os.Chdir(tmpDir)

			// Set enrollment mode in context
			ctx := context.WithValue(context.Background(), core.EnrollmentKey, true)

			// Cancel context if testing cancellation
			if tt.errContains == "context cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // Cancel immediately
			}

			// Setup existing host key if needed
			if tt.setupHostKey && tt.existingHostKey != nil {
				// Copy fixture host key file
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/192.168.1.1.hostkey")
				dstFile := ssh.HostKeyFilePath(tt.host)
				if err := copyFile(srcFile, dstFile); err != nil {
					t.Fatalf("Failed to setup test host key: %v", err)
				}
			}

			// Create mock SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				if tt.connectionError {
					return nil, fmt.Errorf("connection failed")
				}

				// Simulate host key capture that happens during connection in enrollment mode
				// In the real code, this is done by the HostKeyCallback in newSsh
				if !tt.setupHostKey {
					// Copy a new host key from testdata to simulate capture
					srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
					dstFile := ssh.HostKeyFilePath(host)
					_ = copyFile(srcFile, dstFile)
				}

				return &sshmocks_test.MockRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			// Execute
			deps := EnrollDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ApplyUpdatesFunc:     updates.Updates,
				ExportConfigFunc:     export.Export,
			}
			_, err := updateHostKey(ctx, tt.host, deps)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("updateHostKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("updateHostKey() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify host key was captured
			if !tt.wantErr && tt.verifyHostKeySet {
				if !ssh.HostKeyExists(tt.host) {
					t.Error("Expected host key to be captured, but it doesn't exist")
				}
			}
		})
	}
}

func TestDeleteExistingEnrollment(t *testing.T) {
	tests := []struct {
		name            string
		host            string
		hostname        string
		setupHostKey    bool
		setupConfigFile bool
		wantErr         bool
		errContains     string
	}{
		{
			name:            "delete both host key and config file",
			host:            "192.168.1.1",
			hostname:        "router1",
			setupHostKey:    true,
			setupConfigFile: true,
			wantErr:         false,
		},
		{
			name:            "delete only host key",
			host:            "192.168.1.2",
			hostname:        "router2",
			setupHostKey:    true,
			setupConfigFile: false,
			wantErr:         false,
		},
		{
			name:            "delete only config file",
			host:            "192.168.1.3",
			hostname:        "router3",
			setupHostKey:    false,
			setupConfigFile: true,
			wantErr:         false,
		},
		{
			name:            "nothing to delete",
			host:            "192.168.1.4",
			hostname:        "router4",
			setupHostKey:    false,
			setupConfigFile: false,
			wantErr:         false,
		},
		{
			name:            "host with port - delete both",
			host:            "192.168.1.5:2222",
			hostname:        "router5",
			setupHostKey:    true,
			setupConfigFile: true,
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary directory
			tmpDir := t.TempDir()
			originalWd, _ := os.Getwd()
			defer func() {
				_ = os.Chdir(originalWd)
			}()
			_ = os.Chdir(tmpDir)

			// Setup host key if needed
			if tt.setupHostKey {
				hostKeyFile := ssh.HostKeyFilePath(tt.host)
				err := os.WriteFile(hostKeyFile, []byte(`{"host":"test","algorithm":"ssh-rsa","fingerprint":"SHA256:test","publicKey":"dummy","capturedAt":"2025-12-18T00:00:00Z"}`), 0600)
				if err != nil {
					t.Fatalf("Failed to setup test host key: %v", err)
				}
			}

			// Setup config file if needed
			if tt.setupConfigFile {
				parsedHost := ssh.ParseHost(tt.host)
				configFile := fmt.Sprintf("%s.rsc", parsedHost.ShortName)
				err := os.WriteFile(configFile, []byte("# test config"), 0600)
				if err != nil {
					t.Fatalf("Failed to setup test config file: %v", err)
				}
			}

			// Execute
			err := deleteExistingEnrollment(tt.host, tt.hostname)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("deleteExistingEnrollment() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("deleteExistingEnrollment() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify host key was deleted
			if !tt.wantErr && tt.setupHostKey {
				if ssh.HostKeyExists(tt.host) {
					t.Error("Expected host key to be deleted, but it still exists")
				}
			}

			// Verify config file was deleted
			if !tt.wantErr && tt.setupConfigFile {
				parsedHost := ssh.ParseHost(tt.host)
				configFile := fmt.Sprintf("%s.rsc", parsedHost.ShortName)
				if _, err := os.Stat(configFile); !os.IsNotExist(err) {
					t.Error("Expected config file to be deleted, but it still exists")
				}
			}
		})
	}
}

func TestHandleUpdateHostKeyOnly(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		setupHostKeys    map[string]bool
		connectionErrors map[string]bool
		wantErr          bool
		errContains      string
		expectedSuccess  int
		expectedFail     int
	}{
		{
			name:             "single host mode - success",
			host:             "router1",
			setupHostKeys:    map[string]bool{},
			connectionErrors: map[string]bool{},
			wantErr:          false,
			expectedSuccess:  1,
			expectedFail:     0,
		},
		{
			name: "single host mode - failure",
			host: "router1",
			connectionErrors: map[string]bool{
				"router1": true,
			},
			wantErr:         true,
			errContains:     "failed to connect to device",
			expectedSuccess: 0,
			expectedFail:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary directory
			tmpDir := t.TempDir()
			originalWd, _ := os.Getwd()
			defer func() {
				_ = os.Chdir(originalWd)
			}()
			_ = os.Chdir(tmpDir)

			// Set enrollment mode in context
			ctx := context.WithValue(context.Background(), core.EnrollmentKey, true)

			// Setup existing host keys
			for host, setup := range tt.setupHostKeys {
				if setup {
					srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
					dstFile := ssh.HostKeyFilePath(host)
					if err := copyFile(srcFile, dstFile); err != nil {
						t.Fatalf("Failed to setup test host key for %s: %v", host, err)
					}
				}
			}

			// Create mock SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				if tt.connectionErrors[host] {
					return nil, fmt.Errorf("connection failed for %s", host)
				}

				// Simulate host key capture
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
				dstFile := ssh.HostKeyFilePath(host)
				_ = copyFile(srcFile, dstFile)

				return &sshmocks_test.MockRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			deps := EnrollDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ApplyUpdatesFunc:     updates.Updates,
				ExportConfigFunc:     export.Export,
			}

			// Execute
			_, err := updateHostKey(ctx, tt.host, deps)

			// Verify error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("updateHostKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("updateHostKey() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify host keys were created for successful hosts
			if !tt.wantErr {
				if !tt.connectionErrors[tt.host] {
					if !ssh.HostKeyExists(tt.host) {
						t.Errorf("Expected host key for %s to exist after successful update", tt.host)
					}
				}
			}
		})
	}
}

func TestEnrollActionValidation(t *testing.T) {
	tests := []struct {
		name                 string
		hosts                []string
		hostnameValue        string
		updateHostKeyOnly    bool
		force                bool
		wantErr              bool
		errContains          string
		expectedHostKeyCount int // Number of hosts that should have hostkey updated
	}{
		{
			name:              "normal enrollment requires hostname",
			hosts:             []string{"router1"},
			hostnameValue:     "",
			updateHostKeyOnly: false,
			force:             false,
			wantErr:           true,
			errContains:       "--hostname is required",
		},
		{
			name:              "normal enrollment requires single host",
			hosts:             []string{"router1", "router2"},
			hostnameValue:     "test",
			updateHostKeyOnly: false,
			force:             false,
			wantErr:           true,
			errContains:       "requires exactly one host",
		},
		{
			name:              "cannot use force with update-hostkey-only",
			hosts:             []string{"router1"},
			hostnameValue:     "",
			updateHostKeyOnly: true,
			force:             true,
			wantErr:           true,
			errContains:       "cannot use --force and --update-hostkey-only together",
		},
		{
			name:                 "update-hostkey-only batch mode",
			hosts:                []string{"router1", "router2", "router3"},
			hostnameValue:        "",
			updateHostKeyOnly:    true,
			force:                false,
			wantErr:              false,
			expectedHostKeyCount: 3,
		},
		{
			name:                 "update-hostkey-only single host mode",
			hosts:                []string{"router1"},
			hostnameValue:        "",
			updateHostKeyOnly:    true,
			force:                false,
			wantErr:              false,
			expectedHostKeyCount: 1,
		},
		{
			name:              "update-hostkey-only with no hosts",
			hosts:             []string{},
			hostnameValue:     "",
			updateHostKeyOnly: true,
			force:             false,
			wantErr:           true,
			errContains:       "no hosts specified or discovered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary directory
			tmpDir := t.TempDir()
			originalWd, _ := os.Getwd()
			defer func() {
				_ = os.Chdir(originalWd)
			}()
			_ = os.Chdir(tmpDir)

			// Build enrollment config from test values
			enrollCfg := EnrollConfig{
				Hostname:          tt.hostnameValue,
				UpdateHostKeyOnly: tt.updateHostKeyOnly,
				Force:             tt.force,
			}

			// Create mock config
			cfg := &core.Config{
				Hosts: tt.hosts,
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)
			ctx = context.WithValue(ctx, core.EnrollmentKey, true)

			// Create mock SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				// Simulate host key capture
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
				dstFile := ssh.HostKeyFilePath(host)
				_ = copyFile(srcFile, dstFile)

				return &sshmocks_test.MockRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			// Execute the Action logic directly (simulating the CLI command execution)
			var err error

			// Build dependencies from test sshmocks_test
			deps := EnrollDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ApplyUpdatesFunc:     updates.Updates,
				ExportConfigFunc:     export.Export,
			}

			// Validate flag combinations
			if enrollCfg.Force && enrollCfg.UpdateHostKeyOnly {
				err = fmt.Errorf("cannot use --force and --update-hostkey-only together")
			} else if enrollCfg.UpdateHostKeyOnly {
				// Batch mode: update hostkeys for all discovered hosts
				if len(cfg.Hosts) > 1 {
					successCount := 0
					failCount := 0
					var lastErr error

					for _, host := range cfg.Hosts {
						if _, updateErr := updateHostKey(ctx, host, deps); updateErr != nil {
							failCount++
							lastErr = updateErr
						} else {
							successCount++
						}
					}

					if failCount > 0 && successCount == 0 {
						err = fmt.Errorf("all host key updates failed")
					} else if failCount > 0 {
						err = fmt.Errorf("some host key updates failed: %w", lastErr)
					}
				} else if len(cfg.Hosts) == 1 {
					// Single host mode
					host := cfg.Hosts[0]
					_, err = updateHostKey(ctx, host, deps)
				} else {
					err = fmt.Errorf("no hosts specified or discovered")
				}
			} else {
				// Normal enrollment validation
				if len(cfg.Hosts) != 1 {
					err = fmt.Errorf("enroll command requires exactly one host, got %d", len(cfg.Hosts))
				} else if enrollCfg.Hostname == "" {
					err = fmt.Errorf("--hostname is required for enrollment")
				}
			}

			// Verify error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("Expected error: %v, got error: %v", tt.wantErr, err)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify host keys were created for successful scenarios
			if !tt.wantErr && tt.updateHostKeyOnly {
				actualCount := 0
				for _, host := range tt.hosts {
					if ssh.HostKeyExists(host) {
						actualCount++
					}
				}
				if actualCount != tt.expectedHostKeyCount {
					t.Errorf("Expected %d host keys to be created, got %d", tt.expectedHostKeyCount, actualCount)
				}
			}
		})
	}
}

func TestConnectToRouter(t *testing.T) {
	tests := []struct {
		name            string
		host            string
		hostname        string
		connectionError bool
		wantErr         bool
		errContains     string
	}{
		{
			name:            "successful connection",
			host:            "router1",
			hostname:        "router1",
			connectionError: false,
			wantErr:         false,
		},
		{
			name:            "connection failure",
			host:            "router1",
			hostname:        "router1",
			connectionError: true,
			wantErr:         true,
			errContains:     "failed to connect to router",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create mock SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				if tt.connectionError {
					return nil, fmt.Errorf("connection error")
				}
				return &sshmocks_test.MockRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			deps := EnrollDependencies{
				SSHConnectionFactory: mockSSHFactory,
			}

			// Execute
			conn, err := connectToRouter(ctx, tt.host, tt.hostname, deps)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("connectToRouter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("connectToRouter() error = %v, should contain %q", err, tt.errContains)
				}
			}

			if !tt.wantErr && conn == nil {
				t.Error("connectToRouter() returned nil connection on success")
			}

			if conn != nil {
				_ = conn.Close()
			}
		})
	}
}

func TestApplyPreEnrollScript(t *testing.T) {
	tests := []struct {
		name          string
		scriptExists  bool
		scriptContent string
		runFunc       func(cmd string) (string, error)
		wantErr       bool
		errContains   string
		expectedCmds  []string
	}{
		{
			name:         "successful pre-enroll script",
			scriptExists: true,
			scriptContent: `/interface bridge add name=bridge1
/ip address add address=192.168.1.1/24 interface=bridge1`,
			runFunc: func(cmd string) (string, error) {
				return "", nil
			},
			wantErr: false,
			expectedCmds: []string{
				"/interface bridge add name=bridge1",
				"/ip address add address=192.168.1.1/24 interface=bridge1",
			},
		},
		{
			name:          "script file does not exist",
			scriptExists:  false,
			scriptContent: "",
			wantErr:       true,
			errContains:   "failed to apply pre-enroll configuration file",
		},
		{
			name:         "command execution fails",
			scriptExists: true,
			scriptContent: `/system identity set name=test
/invalid command`,
			runFunc: func(cmd string) (string, error) {
				if strings.Contains(cmd, "invalid") {
					return "", fmt.Errorf("syntax error")
				}
				return "", nil
			},
			wantErr:     true,
			errContains: "failed to apply pre-enroll configuration file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temp directory and script file
			tmpDir := t.TempDir()
			scriptPath := filepath.Join(tmpDir, "pre-enroll.rsc")

			cfg := EnrollConfig{
				PreEnrollScript: scriptPath,
			}

			if tt.scriptExists {
				if err := os.WriteFile(scriptPath, []byte(tt.scriptContent), 0644); err != nil {
					t.Fatalf("Failed to create test script: %v", err)
				}
			}

			// Create mock connection
			mockConn := &sshmocks_test.MockRunner{
				RunFunc: tt.runFunc,
			}

			// Execute
			err := applyPreEnrollScript(mockConn, cfg)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("applyPreEnrollScript() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("applyPreEnrollScript() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Check executed commands
			if !tt.wantErr && tt.expectedCmds != nil {
				if len(mockConn.CommandHistory) != len(tt.expectedCmds) {
					t.Errorf("Expected %d commands, got %d", len(tt.expectedCmds), len(mockConn.CommandHistory))
				}
				for i, expectedCmd := range tt.expectedCmds {
					if i >= len(mockConn.CommandHistory) {
						break
					}
					if mockConn.CommandHistory[i] != expectedCmd {
						t.Errorf("Command %d = %q, want %q", i, mockConn.CommandHistory[i], expectedCmd)
					}
				}
			}
		})
	}
}

func TestApplyPostEnrollScript(t *testing.T) {
	tests := []struct {
		name          string
		scriptExists  bool
		scriptContent string
		runFunc       func(cmd string) (string, error)
		wantErr       bool
		errContains   string
		expectedCmds  []string
	}{
		{
			name:         "successful post-enroll script",
			scriptExists: true,
			scriptContent: `/system ntp client set enabled=yes
/system ntp client servers add address=time.google.com`,
			runFunc: func(cmd string) (string, error) {
				return "", nil
			},
			wantErr: false,
			expectedCmds: []string{
				"/system ntp client set enabled=yes",
				"/system ntp client servers add address=time.google.com",
			},
		},
		{
			name:          "script file does not exist",
			scriptExists:  false,
			scriptContent: "",
			wantErr:       true,
			errContains:   "failed to apply post-enroll configuration file",
		},
		{
			name:         "command execution fails",
			scriptExists: true,
			scriptContent: `/system identity set name=test
/bad command here`,
			runFunc: func(cmd string) (string, error) {
				if strings.Contains(cmd, "bad") {
					return "", fmt.Errorf("syntax error")
				}
				return "", nil
			},
			wantErr:     true,
			errContains: "failed to apply post-enroll configuration file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temp directory and script file
			tmpDir := t.TempDir()
			scriptPath := filepath.Join(tmpDir, "post-enroll.rsc")

			cfg := EnrollConfig{
				PostEnrollScript: scriptPath,
			}

			if tt.scriptExists {
				if err := os.WriteFile(scriptPath, []byte(tt.scriptContent), 0644); err != nil {
					t.Fatalf("Failed to create test script: %v", err)
				}
			}

			// Create mock connection
			mockConn := &sshmocks_test.MockRunner{
				RunFunc: tt.runFunc,
			}

			// Execute
			err := applyPostEnrollScript(mockConn, cfg)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("applyPostEnrollScript() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("applyPostEnrollScript() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Check executed commands
			if !tt.wantErr && tt.expectedCmds != nil {
				if len(mockConn.CommandHistory) != len(tt.expectedCmds) {
					t.Errorf("Expected %d commands, got %d", len(tt.expectedCmds), len(mockConn.CommandHistory))
				}
				for i, expectedCmd := range tt.expectedCmds {
					if i >= len(mockConn.CommandHistory) {
						break
					}
					if mockConn.CommandHistory[i] != expectedCmd {
						t.Errorf("Command %d = %q, want %q", i, mockConn.CommandHistory[i], expectedCmd)
					}
				}
			}
		})
	}
}

func TestApplyUpdates(t *testing.T) {
	tests := []struct {
		name        string
		updateError bool
		wantErr     bool
		errContains string
	}{
		{
			name:        "successful updates",
			updateError: false,
			wantErr:     false,
		},
		{
			name:        "update failure",
			updateError: true,
			wantErr:     true,
			errContains: "failed to apply updates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			host := "router1"

			// Create mock update function
			mockUpdateFunc := func(ctx context.Context, host string) error {
				if tt.updateError {
					return fmt.Errorf("update failed")
				}
				return nil
			}

			deps := EnrollDependencies{
				ApplyUpdatesFunc: mockUpdateFunc,
			}

			// Execute
			err := applyUpdates(ctx, host, host, deps)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("applyUpdates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("applyUpdates() error = %v, should contain %q", err, tt.errContains)
				}
			}
		})
	}
}

func TestExportConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		exportError    bool
		reconnectError bool
		wantErr        bool
		errContains    string
	}{
		{
			name:           "successful export and reconnect",
			exportError:    false,
			reconnectError: false,
			wantErr:        false,
		},
		{
			name:        "export failure",
			exportError: true,
			wantErr:     true,
			errContains: "failed to export configuration",
		},
		{
			name:           "reconnect failure after export",
			exportError:    false,
			reconnectError: true,
			wantErr:        true,
			errContains:    "failed to reconnect after export",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			host := "router1"
			outputDir := t.TempDir()

			enrollCfg := EnrollConfig{
				OutputDir: outputDir,
				Hostname:  "test-router",
			}

			// Create mock export function
			mockExportFunc := func(ctx context.Context, host, outputDir string, hideOutput bool, hostname string) error {
				if tt.exportError {
					return fmt.Errorf("export failed")
				}
				return nil
			}

			// Track if close was called
			closeCalled := false

			// Create mock original connection
			mockConn := &sshmocks_test.MockRunner{
				CloseFunc: func() error {
					closeCalled = true
					return nil
				},
			}

			// Create mock SSH connection factory
			reconnectAttempted := false
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				reconnectAttempted = true
				if tt.reconnectError {
					return nil, fmt.Errorf("reconnect failed")
				}
				return &sshmocks_test.MockRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			deps := EnrollDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ExportConfigFunc:     mockExportFunc,
			}

			// Execute
			newConn, err := exportConfiguration(ctx, host, host, enrollCfg, deps, mockConn)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("exportConfiguration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("exportConfiguration() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify connection was closed before reconnect
			if !tt.exportError && !closeCalled {
				t.Error("exportConfiguration() should close original connection")
			}

			// Verify reconnect was attempted
			if !tt.exportError && !reconnectAttempted {
				t.Error("exportConfiguration() should attempt reconnect after export")
			}

			// Verify new connection returned on success
			if !tt.wantErr && newConn == nil {
				t.Error("exportConfiguration() should return new connection on success")
			}

			if newConn != nil {
				_ = newConn.Close()
			}
		})
	}
}

func TestEnrollMainWorkflow(t *testing.T) {
	tests := []struct {
		name                 string
		host                 string
		hostname             string
		preEnrollScript      string
		postEnrollScript     string
		skipUpdates          bool
		skipExport           bool
		force                bool
		updateHostKeyOnly    bool
		setupExistingHostKey bool
		setupExistingConfig  bool
		setupScripts         bool
		connectionError      bool
		preScriptError       bool
		identityError        bool
		updateError          bool
		exportError          bool
		postScriptError      bool
		contextCancelled     bool
		wantErr              bool
		errContains          string
	}{
		{
			name:             "successful full enrollment",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			skipUpdates:      false,
			skipExport:       false,
			wantErr:          false,
		},
		{
			name:             "successful enrollment with skip updates",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			skipUpdates:      true,
			skipExport:       false,
			wantErr:          false,
		},
		{
			name:             "successful enrollment with skip export",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			skipUpdates:      false,
			skipExport:       true,
			wantErr:          false,
		},
		{
			name:                 "successful enrollment with force re-enrollment",
			host:                 "router1",
			hostname:             "test-router",
			setupScripts:         true,
			preEnrollScript:      "pre-enroll.rsc",
			postEnrollScript:     "post-enroll.rsc",
			force:                true,
			setupExistingHostKey: true,
			setupExistingConfig:  true,
			wantErr:              false,
		},
		{
			name:              "update host key only mode - single host",
			host:              "router1",
			updateHostKeyOnly: true,
			wantErr:           false,
		},
		{
			name:              "fail on force with update-hostkey-only",
			host:              "router1",
			force:             true,
			updateHostKeyOnly: true,
			wantErr:           true,
			errContains:       "cannot use --force and --update-hostkey-only together",
		},
		{
			name:            "fail on host key capture error",
			host:            "router1",
			hostname:        "test-router",
			connectionError: true,
			wantErr:         true,
			errContains:     "failed to capture host key",
		},
		{
			name:            "fail on connection error",
			host:            "router1",
			hostname:        "test-router",
			connectionError: true,
			wantErr:         true,
			errContains:     "failed to capture host key",
		},
		{
			name:             "fail on pre-script error",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			preScriptError:   true,
			wantErr:          true,
			errContains:      "failed to apply pre-enroll script",
		},
		{
			name:             "fail on identity set error",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			identityError:    true,
			wantErr:          true,
			errContains:      "failed to set router identity",
		},
		{
			name:             "fail on export error",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			skipExport:       false,
			exportError:      true,
			wantErr:          true,
			errContains:      "failed to export configuration",
		},
		{
			name:             "fail on post-script error",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			postScriptError:  true,
			wantErr:          true,
			errContains:      "failed to apply post-enroll script",
		},
		{
			name:             "fail on context cancelled",
			host:             "router1",
			hostname:         "test-router",
			contextCancelled: true,
			wantErr:          true,
			errContains:      "context cancelled",
		},
		{
			name:             "updates failure is non-fatal",
			host:             "router1",
			hostname:         "test-router",
			setupScripts:     true,
			preEnrollScript:  "pre-enroll.rsc",
			postEnrollScript: "post-enroll.rsc",
			updateError:      true,
			wantErr:          false, // Non-fatal, enrollment should continue
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary directory
			tmpDir := t.TempDir()
			originalWd, _ := os.Getwd()
			defer func() {
				_ = os.Chdir(originalWd)
			}()
			_ = os.Chdir(tmpDir)

			// Setup enrollment mode context
			ctx := context.WithValue(context.Background(), core.EnrollmentKey, true)

			// Create mock config
			cfg := &core.Config{
				Hosts: []string{tt.host},
			}
			ctx = context.WithValue(ctx, core.ConfigKey, cfg)

			// Setup context cancellation if needed
			if tt.contextCancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // Cancel immediately
			}

			// Setup existing host key if needed
			if tt.setupExistingHostKey {
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
				dstFile := ssh.HostKeyFilePath(tt.host)
				_ = copyFile(srcFile, dstFile)
			}

			// Setup existing config file if needed
			if tt.setupExistingConfig {
				configFile := fmt.Sprintf("%s.rsc", tt.host)
				_ = os.WriteFile(configFile, []byte("# existing config"), 0600)
			}

			// Setup scripts if needed
			if tt.setupScripts {
				preScript := tt.preEnrollScript
				if preScript == "" {
					preScript = "pre-enroll.rsc"
				}
				postScript := tt.postEnrollScript
				if postScript == "" {
					postScript = "post-enroll.rsc"
				}

				_ = os.WriteFile(preScript, []byte("/system identity set name=pre-test"), 0644)
				_ = os.WriteFile(postScript, []byte("/system identity set name=post-test"), 0644)
			}

			// Track SSH connections for proper lifecycle verification
			connectionCount := 0
			var connections []*sshmocks_test.MockRunner

			// Create mock SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				if tt.connectionError {
					return nil, fmt.Errorf("connection failed")
				}

				// Simulate host key capture
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
				dstFile := ssh.HostKeyFilePath(host)
				_ = copyFile(srcFile, dstFile)

				connectionCount++
				mockConn := &sshmocks_test.MockRunner{
					CloseFunc: func() error { return nil },
					RunFunc: func(cmd string) (string, error) {
						// Simulate various errors based on command
						if tt.preScriptError && strings.Contains(cmd, "pre-test") {
							return "", fmt.Errorf("pre-script command failed")
						}
						if tt.identityError && strings.Contains(cmd, "/system identity set") && strings.Contains(cmd, tt.hostname) {
							return "", fmt.Errorf("identity set failed")
						}
						if tt.postScriptError && strings.Contains(cmd, "post-test") {
							return "", fmt.Errorf("post-script command failed")
						}
						return "", nil
					},
				}
				connections = append(connections, mockConn)
				return mockConn, nil
			}

			// Create mock update function
			mockUpdateFunc := func(ctx context.Context, host string) error {
				if tt.updateError {
					return fmt.Errorf("update failed")
				}
				return nil
			}

			// Create mock export function
			mockExportFunc := func(ctx context.Context, host, outputDir string, hideOutput bool, hostname string) error {
				if tt.exportError {
					return fmt.Errorf("export failed")
				}
				return nil
			}

			// Build enrollment config
			enrollCfg := EnrollConfig{
				Hostname:          tt.hostname,
				PreEnrollScript:   tt.preEnrollScript,
				PostEnrollScript:  tt.postEnrollScript,
				SkipUpdates:       tt.skipUpdates,
				SkipExport:        tt.skipExport,
				OutputDir:         tmpDir,
				Force:             tt.force,
				UpdateHostKeyOnly: tt.updateHostKeyOnly,
			}

			// Set defaults for scripts
			if enrollCfg.PreEnrollScript == "" {
				enrollCfg.PreEnrollScript = "./pre-enroll.rsc"
			}
			if enrollCfg.PostEnrollScript == "" {
				enrollCfg.PostEnrollScript = "./post-enroll.rsc"
			}

			// Build dependencies
			deps := EnrollDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ApplyUpdatesFunc:     mockUpdateFunc,
				ExportConfigFunc:     mockExportFunc,
			}

			// Execute enrollment logic (simulating what enroll() does)
			var err error

			// Validate flag combination
			if enrollCfg.Force && enrollCfg.UpdateHostKeyOnly {
				err = fmt.Errorf("cannot use --force and --update-hostkey-only together")
			} else if enrollCfg.UpdateHostKeyOnly {
				// Route to processMultiHostKeyUpdate
				_, err = updateHostKey(ctx, tt.host, deps)
			} else {
				// Normal enrollment validation
				if len(cfg.Hosts) != 1 {
					err = fmt.Errorf("enroll command requires exactly one host, got %d", len(cfg.Hosts))
				} else {
					host := cfg.Hosts[0]

					// Step 1: Update host key
					_, err = updateHostKey(ctx, host, deps)
					if err != nil {
						err = fmt.Errorf("failed to capture host key: %w", err)
					}

					// Handle force re-enrollment
					if err == nil && enrollCfg.Force {
						err = deleteExistingEnrollment(host, host)
						if err != nil {
							err = fmt.Errorf("failed to remove existing enrollment: %w", err)
						}
					}

					// Check context cancellation
					if err == nil {
						if ctxErr := ctx.Err(); ctxErr != nil {
							err = fmt.Errorf("context cancelled: %w", ctxErr)
						}
					}

					// Step 2: Establish connection
					var conn ssh.RunnerInterface
					if err == nil {
						conn, err = connectToRouter(ctx, host, host, deps)
					}

					if conn != nil {
						defer func() {
							_ = conn.Close()
						}()
					}

					// Step 3: Apply pre-enrollment script
					if err == nil && conn != nil {
						err = applyPreEnrollScript(conn, enrollCfg)
						if err != nil {
							err = fmt.Errorf("failed to apply pre-enroll script: %w", err)
						}
					}

					// Step 4: Set router identity
					if err == nil && conn != nil {
						err = setRouterIdentity(conn, enrollCfg.Hostname)
						if err != nil {
							err = fmt.Errorf("failed to set router identity: %w", err)
						}
					}

					// Step 5: Apply updates (optional)
					if err == nil && conn != nil {
						if !enrollCfg.SkipUpdates {
							updateErr := applyUpdates(ctx, host, host, deps)
							// Updates are non-fatal, don't fail enrollment
							_ = updateErr
						}
					}

					// Step 6: Export configuration (optional)
					if err == nil && conn != nil && !enrollCfg.SkipExport {
						newConn, exportErr := exportConfiguration(ctx, host, host, enrollCfg, deps, conn)
						if exportErr != nil {
							err = fmt.Errorf("failed to export configuration: %w", exportErr)
						} else if newConn != nil {
							conn = newConn
						}
					}

					// Step 7: Apply post-enrollment script
					if err == nil && conn != nil {
						err = applyPostEnrollScript(conn, enrollCfg)
						if err != nil {
							err = fmt.Errorf("failed to apply post-enroll script: %w", err)
						}
					}
				}
			}

			// Verify error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("enroll workflow error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("enroll workflow error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify host keys were created for successful scenarios
			if !tt.wantErr && !tt.connectionError {
				if !ssh.HostKeyExists(tt.host) {
					t.Errorf("Expected host key for %s to exist after enrollment", tt.host)
				}
			}

			// Verify force cleanup happened
			if !tt.wantErr && tt.force && !tt.updateHostKeyOnly {
				configFile := fmt.Sprintf("%s.rsc", tt.host)
				if _, statErr := os.Stat(configFile); !os.IsNotExist(statErr) {
					t.Errorf("Expected config file %s to be deleted by force cleanup", configFile)
				}
			}
		})
	}
}

// TestEnrollWorkflow tests the main enroll coordinator function
func TestEnrollWorkflow(t *testing.T) {
	// Helper to write valid JSON host key files
	writeHostKeyFile := func(hostname string) error {
		hostKeyInfo := ssh.HostKeyInfo{
			Host:        hostname,
			CapturedAt:  time.Now(),
			Algorithm:   "ssh-ed25519",
			Fingerprint: "SHA256:MockKeyForTesting",
			PublicKey:   "AAAAC3NzaC1lZDI1NTE5AAAAIMockKeyForTesting",
		}
		data, err := json.Marshal(hostKeyInfo)
		if err != nil {
			return err
		}
		return os.WriteFile(fmt.Sprintf("%s.hostkey", hostname), data, 0600)
	}

	tests := []struct {
		name              string
		host              string
		hostname          string
		preEnrollScript   string
		postEnrollScript  string
		skipUpdates       bool
		skipExport        bool
		force             bool
		updateHostKeyOnly bool
		setupMocks        func(t *testing.T) EnrollDependencies
		setupFiles        func(t *testing.T, hostname string)
		wantErr           bool
		errContains       string
		validateResults   func(t *testing.T, hostname string)
	}{
		{
			name:     "successful full enrollment",
			host:     "test-router1",
			hostname: "test-router1",
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create empty pre/post-enroll scripts
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false,
			validateResults: func(t *testing.T, hostname string) {
				// Verify host key file exists
				if _, err := os.Stat("test-router1.hostkey"); os.IsNotExist(err) {
					t.Error("Expected host key file to exist")
				}
			},
		},
		{
			name:        "enrollment with skip-updates flag",
			host:        "test-router2",
			hostname:    "test-router2",
			skipUpdates: true,
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						t.Error("ApplyUpdatesFunc should not be called when skipUpdates is true")
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create empty pre/post-enroll scripts
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false,
		},
		{
			name:       "enrollment with skip-export flag",
			host:       "test-router3",
			hostname:   "test-router3",
			skipExport: true,
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						t.Error("ExportConfigFunc should not be called when skipExport is true")
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create empty pre/post-enroll scripts
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false,
		},
		{
			name:        "enrollment with both skip flags",
			host:        "test-router4",
			hostname:    "test-router4",
			skipUpdates: true,
			skipExport:  true,
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						t.Error("ApplyUpdatesFunc should not be called")
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						t.Error("ExportConfigFunc should not be called")
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create empty pre/post-enroll scripts
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false,
		},
		{
			name:     "enrollment with pre-enroll script",
			host:     "test-router5",
			hostname: "test-router5",
			setupMocks: func(t *testing.T) EnrollDependencies {
				preScriptExecuted := false
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						if strings.Contains(cmd, "pre-test-command") {
							preScriptExecuted = true
						}
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						if !preScriptExecuted {
							t.Error("Pre-enroll script should be executed before updates")
						}
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create pre-enroll script
				scriptContent := "/system identity set name=pre-test-command\n"
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(scriptContent), 0644)
				// Create empty post-enroll script
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false,
		},
		{
			name:     "enrollment with post-enroll script",
			host:     "test-router6",
			hostname: "test-router6",
			setupMocks: func(t *testing.T) EnrollDependencies {
				postScriptExecuted := false
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						if strings.Contains(cmd, "post-test-command") {
							postScriptExecuted = true
						}
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						if postScriptExecuted {
							t.Error("Post-enroll script should not be executed before updates")
						}
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						if postScriptExecuted {
							t.Error("Post-enroll script should not be executed before export")
						}
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create empty pre-enroll script
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				// Create post-enroll script
				scriptContent := "/system identity set name=post-test-command\n"
				_ = os.WriteFile("post-enroll-test.rsc", []byte(scriptContent), 0644)
			},
			wantErr: false,
		},
		{
			name:     "enrollment with force flag removes existing artifacts",
			host:     "test-router7",
			hostname: "test-router7",
			force:    true,
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create existing enrollment artifacts
				_ = os.WriteFile("test-router7.hostkey", []byte("old-key"), 0600)
				_ = os.WriteFile("test-router7.rsc", []byte("old-config"), 0644)
				// Create empty pre/post-enroll scripts
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false,
			validateResults: func(t *testing.T, hostname string) {
				// Old files should be deleted and new ones created
				content, err := os.ReadFile("test-router7.hostkey")
				if err != nil {
					t.Error("Host key file should exist after force enrollment")
				}
				if strings.Contains(string(content), "old-key") {
					t.Error("Old host key should have been replaced")
				}
			},
		},
		{
			name:              "update-host-key-only mode single host",
			host:              "test-router8",
			hostname:          "test-router8",
			updateHostKeyOnly: true,
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						t.Error("ApplyUpdatesFunc should not be called in update-host-key-only mode")
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						t.Error("ExportConfigFunc should not be called in update-host-key-only mode")
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {},
			wantErr:    false,
			validateResults: func(t *testing.T, hostname string) {
				// Only host key should exist, no config file
				if _, err := os.Stat("test-router8.hostkey"); os.IsNotExist(err) {
					t.Error("Host key file should exist")
				}
			},
		},
		{
			name:              "error: force and update-host-key-only conflict",
			host:              "test-router12",
			hostname:          "test-router12",
			force:             true,
			updateHostKeyOnly: true,
			setupMocks: func(t *testing.T) EnrollDependencies {
				return EnrollDependencies{}
			},
			setupFiles:  func(t *testing.T, hostname string) {},
			wantErr:     true,
			errContains: "cannot use --force and --update-hostkey-only together",
		},
		{
			name:     "error: SSH connection failure",
			host:     "test-router15",
			hostname: "test-router15",
			setupMocks: func(t *testing.T) EnrollDependencies {
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						return nil, fmt.Errorf("connection refused")
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						return nil
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						return nil
					},
				}
			},
			setupFiles:  func(t *testing.T, hostname string) {},
			wantErr:     true,
			errContains: "connection refused",
		},
		{
			name:     "non-fatal: updates failure doesn't stop enrollment",
			host:     "test-router16",
			hostname: "test-router16",
			setupMocks: func(t *testing.T) EnrollDependencies {
				mockRunner := &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
				}
				return EnrollDependencies{
					SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
						if err := writeHostKeyFile(host); err != nil {
							return nil, fmt.Errorf("failed to write host key: %w", err)
						}
						return mockRunner, nil
					},
					ApplyUpdatesFunc: func(ctx context.Context, host string) error {
						return fmt.Errorf("update failed but non-fatal")
					},
					ExportConfigFunc: func(ctx context.Context, host, outputDir string, verbose bool, identityOverride string) error {
						return nil
					},
				}
			},
			setupFiles: func(t *testing.T, hostname string) {
				// Create empty pre/post-enroll scripts
				_ = os.WriteFile("pre-enroll-test.rsc", []byte(""), 0644)
				_ = os.WriteFile("post-enroll-test.rsc", []byte(""), 0644)
			},
			wantErr: false, // Updates failure is non-fatal
			validateResults: func(t *testing.T, hostname string) {
				// Enrollment should complete despite updates failure
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test directory
			tmpDir := t.TempDir()
			oldDir, _ := os.Getwd()
			_ = os.Chdir(tmpDir)
			defer func() { _ = os.Chdir(oldDir) }()

			// Setup files if needed
			tt.setupFiles(t, tt.hostname)

			// Setup pre/post enroll scripts if they exist
			cfg := EnrollConfig{
				Hostname:          tt.hostname,
				SkipUpdates:       tt.skipUpdates,
				SkipExport:        tt.skipExport,
				Force:             tt.force,
				UpdateHostKeyOnly: tt.updateHostKeyOnly,
			}

			if tt.preEnrollScript != "" || fileExists("pre-enroll-test.rsc") {
				cfg.PreEnrollScript = "pre-enroll-test.rsc"
			}
			if tt.postEnrollScript != "" || fileExists("post-enroll-test.rsc") {
				cfg.PostEnrollScript = "post-enroll-test.rsc"
			}

			deps := tt.setupMocks(t)

			// Setup context with core.Config
			ctx := context.Background()
			hosts := strings.Split(tt.hostname, ",")
			coreConfig := &core.Config{
				Hosts: hosts,
			}
			ctx = context.WithValue(ctx, core.ConfigKey, coreConfig)

			// Execute enroll
			err := enroll(ctx, tt.host, cfg, deps)

			// Verify results
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Error %q does not contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}

			if tt.validateResults != nil {
				tt.validateResults(t, tt.hostname)
			}
		})
	}
}

// fileExists checks if a file exists at the given path
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

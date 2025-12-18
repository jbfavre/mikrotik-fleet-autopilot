package enroll

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/core"
)

// MockSshRunner is a mock implementation of SshRunner for testing
type MockSshRunner struct {
	CloseFunc                func() error
	IsAlreadyClosedErrorFunc func(err error) bool
	RunFunc                  func(cmd string) (string, error)
	commandHistory           []string
}

func (m *MockSshRunner) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

func (m *MockSshRunner) IsAlreadyClosedError(err error) bool {
	if m.IsAlreadyClosedErrorFunc != nil {
		return m.IsAlreadyClosedErrorFunc(err)
	}
	return false
}

func (m *MockSshRunner) Run(cmd string) (string, error) {
	m.commandHistory = append(m.commandHistory, cmd)
	if m.RunFunc != nil {
		return m.RunFunc(cmd)
	}
	return "", nil
}

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
			mockConn := &MockSshRunner{
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
				if len(mockConn.commandHistory) != len(tt.expectedCmds) {
					t.Errorf("Expected %d commands, got %d", len(tt.expectedCmds), len(mockConn.commandHistory))
				}
				for i, expectedCmd := range tt.expectedCmds {
					if i >= len(mockConn.commandHistory) {
						t.Errorf("Missing command at index %d: %s", i, expectedCmd)
						continue
					}
					if mockConn.commandHistory[i] != expectedCmd {
						t.Errorf("Command %d = %q, want %q", i, mockConn.commandHistory[i], expectedCmd)
					}
				}
			}
		})
	}
}

func TestApplyConfigFileInvalidFile(t *testing.T) {
	mockConn := &MockSshRunner{}
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &MockSshRunner{
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
				if len(mockConn.commandHistory) != 1 {
					t.Errorf("Expected 1 command, got %d", len(mockConn.commandHistory))
				} else if mockConn.commandHistory[0] != tt.expectedCmd {
					t.Errorf("Command = %q, want %q", mockConn.commandHistory[0], tt.expectedCmd)
				}
			}
		})
	}
}

func TestEnroll(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		hostnameValue    string
		setupPreConfig   func() string
		setupPostConfig  func() string
		skipUpdatesValue bool
		skipExportValue  bool
		connectionError  error
		commandErrors    map[string]error
		updatesError     error
		exportError      error
		wantErr          bool
		errContains      string
	}{
		{
			name:             "successful enrollment",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-success.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-success.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			wantErr: false,
		},
		{
			name:             "successful enrollment with updates",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: false,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-with-updates.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-with-updates.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			wantErr: false,
		},
		{
			name:             "successful enrollment with export",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  false,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-with-export.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-with-export.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			wantErr: false,
		},
		{
			name:             "successful enrollment with updates and export",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: false,
			skipExportValue:  false,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-full.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-full.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			wantErr: false,
		},
		{
			name:             "updates failure (non-fatal)",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: false,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-updatefail.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-updatefail.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			updatesError: fmt.Errorf("update check failed"),
			wantErr:      false, // Updates failure is non-fatal
		},
		{
			name:             "export failure (non-fatal)",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  false,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-exportfail.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-exportfail.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			exportError: fmt.Errorf("export failed"),
			wantErr:     true, // Export failure is non-fatal
		},
		{
			name:             "connection failure",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			connectionError:  fmt.Errorf("failed to connect to host"),
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-connfail.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-connfail.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			wantErr:     true,
			errContains: "failed to connect to host",
		},
		{
			name:             "pre-enroll config file application error",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-configerr.rsc")
				_ = os.WriteFile(configFile, []byte("/invalid command"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-configerr.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			commandErrors: map[string]error{
				"/invalid command": fmt.Errorf("syntax error"),
			},
			wantErr:     true,
			errContains: "failed to apply pre-enroll configuration file",
		},
		{
			name:             "identity set error",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-identityerr.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-identityerr.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			commandErrors: map[string]error{
				"/system identity set name=test-router": fmt.Errorf("permission denied"),
			},
			wantErr:     true,
			errContains: "failed to set router identity",
		},
		{
			name:             "post-enroll config file application error",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-post-configerr.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-post-configerr.rsc")
				_ = os.WriteFile(configFile, []byte("/invalid post command"), 0644)
				return configFile
			},
			commandErrors: map[string]error{
				"/invalid post command": fmt.Errorf("syntax error"),
			},
			wantErr:     true,
			errContains: "failed to apply post-enroll configuration file",
		},
		{
			name:             "missing post-enroll config file",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-pre-enroll-missing-post.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=pre-test"), 0644)
				return configFile
			},
			setupPostConfig: func() string {
				return "/nonexistent/post-enroll-file.rsc"
			},
			wantErr:     true,
			errContains: "failed to apply post-enroll configuration file",
		},
		{
			name:             "missing pre-enroll config file",
			host:             "192.168.1.50",
			hostnameValue:    "test-router",
			skipUpdatesValue: true,
			skipExportValue:  true,
			setupPreConfig: func() string {
				return "/nonexistent/pre-enroll-file.rsc"
			},
			setupPostConfig: func() string {
				tmpDir := os.TempDir()
				configFile := filepath.Join(tmpDir, "test-post-enroll-missing-pre.rsc")
				_ = os.WriteFile(configFile, []byte("/system note set note=post-test"), 0644)
				return configFile
			},
			wantErr:     true,
			errContains: "failed to apply pre-enroll configuration file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup - set all package variables
			preEnrollScript = tt.setupPreConfig()
			postEnrollScript = tt.setupPostConfig()
			hostname = tt.hostnameValue
			skipUpdates = tt.skipUpdatesValue
			skipExport = tt.skipExportValue
			outputDir = "."

			// Mock SSH connection factory
			originalFactory := sshConnectionFactory
			defer func() { sshConnectionFactory = originalFactory }()

			// Mock updates function
			originalUpdatesFunc := applyUpdatesFunc
			updatesCallCount := 0
			applyUpdatesFunc = func(ctx context.Context, host string) error {
				updatesCallCount++
				if tt.updatesError != nil {
					return tt.updatesError
				}
				return nil
			}
			defer func() { applyUpdatesFunc = originalUpdatesFunc }()

			// Mock export function
			originalExportFunc := exportConfigFunc
			exportCallCount := 0
			exportConfigFunc = func(ctx context.Context, host string, outputDir string, showSensitive bool) error {
				exportCallCount++
				if tt.exportError != nil {
					return tt.exportError
				}
				return nil
			}
			defer func() { exportConfigFunc = originalExportFunc }()

			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				if tt.connectionError != nil {
					return nil, tt.connectionError
				}

				return &MockSshRunner{
					RunFunc: func(cmd string) (string, error) {
						if tt.commandErrors != nil {
							if err, exists := tt.commandErrors[cmd]; exists {
								return "", err
							}
						}
						return "", nil
					},
				}, nil
			}

			// Create context
			ctx := context.Background()

			// Test enroll
			err := enroll(ctx, tt.host)

			// Verify
			if (err != nil) != tt.wantErr {
				t.Errorf("enroll() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("enroll() error = %v, should contain %q", err, tt.errContains)
				}
			}

			// Verify updates was called when expected
			if !tt.wantErr && !tt.skipUpdatesValue {
				if updatesCallCount != 1 {
					t.Errorf("Expected updates to be called once, got %d calls", updatesCallCount)
				}
			} else if !tt.wantErr && tt.skipUpdatesValue {
				if updatesCallCount != 0 {
					t.Errorf("Expected updates not to be called, got %d calls", updatesCallCount)
				}
			}

			// Verify export was called when expected
			if !tt.wantErr && !tt.skipExportValue {
				if exportCallCount != 1 {
					t.Errorf("Expected export to be called once, got %d calls", exportCallCount)
				}
			} else if !tt.wantErr && tt.skipExportValue {
				if exportCallCount != 0 {
					t.Errorf("Expected export not to be called, got %d calls", exportCallCount)
				}
			}
		})
	}
}

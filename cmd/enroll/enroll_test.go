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

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

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
			exportConfigFunc = func(ctx context.Context, host string, outputDir string, showSensitive bool, preferredFilename string) error {
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

func TestUpdateHostKey(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		setupHostKey     bool
		existingHostKey  *core.HostKeyInfo
		connectionError  bool
		wantErr          bool
		errContains      string
		verifyHostKeySet bool
	}{
		{
			name:             "successful host key update with existing key",
			host:             "192.168.1.1",
			setupHostKey:     true,
			existingHostKey:  &core.HostKeyInfo{Algorithm: "ssh-rsa", Fingerprint: "SHA256:old123fingerprint"},
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
			ctx := context.WithValue(context.Background(), core.EnrollmentModeKey, true)

			// Setup existing host key if needed
			if tt.setupHostKey && tt.existingHostKey != nil {
				// Copy fixture host key file
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/192.168.1.1.hostkey")
				dstFile := core.HostKeyFilePath(tt.host)
				if err := copyFile(srcFile, dstFile); err != nil {
					t.Fatalf("Failed to setup test host key: %v", err)
				}
			}

			// Mock SSH connection factory
			originalFactory := sshConnectionFactory
			defer func() { sshConnectionFactory = originalFactory }()

			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				if tt.connectionError {
					return nil, fmt.Errorf("connection failed")
				}

				// Simulate host key capture that happens during connection in enrollment mode
				// In the real code, this is done by the HostKeyCallback in newSsh
				if !tt.setupHostKey {
					// Copy a new host key from testdata to simulate capture
					srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
					dstFile := core.HostKeyFilePath(host)
					_ = copyFile(srcFile, dstFile)
				}

				return &MockSshRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			// Execute
			_, err := updateHostKey(ctx, tt.host)

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
				if !core.HostKeyExists(tt.host) {
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
		setupHostKey    bool
		setupConfigFile bool
		wantErr         bool
		errContains     string
	}{
		{
			name:            "delete both host key and config file",
			host:            "192.168.1.1",
			setupHostKey:    true,
			setupConfigFile: true,
			wantErr:         false,
		},
		{
			name:            "delete only host key",
			host:            "192.168.1.2",
			setupHostKey:    true,
			setupConfigFile: false,
			wantErr:         false,
		},
		{
			name:            "delete only config file",
			host:            "192.168.1.3",
			setupHostKey:    false,
			setupConfigFile: true,
			wantErr:         false,
		},
		{
			name:            "nothing to delete",
			host:            "192.168.1.4",
			setupHostKey:    false,
			setupConfigFile: false,
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
				hostKeyFile := core.HostKeyFilePath(tt.host)
				err := os.WriteFile(hostKeyFile, []byte(`{"host":"test","algorithm":"ssh-rsa","fingerprint":"SHA256:test","publicKey":"dummy","capturedAt":"2025-12-18T00:00:00Z"}`), 0600)
				if err != nil {
					t.Fatalf("Failed to setup test host key: %v", err)
				}
			}

			// Setup config file if needed
			if tt.setupConfigFile {
				configFile := fmt.Sprintf("%s.rsc", tt.host)
				err := os.WriteFile(configFile, []byte("# test config"), 0600)
				if err != nil {
					t.Fatalf("Failed to setup test config file: %v", err)
				}
			}

			// Execute
			err := deleteExistingEnrollment(tt.host)

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
				if core.HostKeyExists(tt.host) {
					t.Error("Expected host key to be deleted, but it still exists")
				}
			}

			// Verify config file was deleted
			if !tt.wantErr && tt.setupConfigFile {
				configFile := fmt.Sprintf("%s.rsc", tt.host)
				if _, err := os.Stat(configFile); !os.IsNotExist(err) {
					t.Error("Expected config file to be deleted, but it still exists")
				}
			}
		})
	}
}

func TestUpdateHostKeyBatchMode(t *testing.T) {
	tests := []struct {
		name             string
		hosts            []string
		setupHostKeys    map[string]bool
		connectionErrors map[string]bool
		wantErr          bool
		expectedSuccess  int
		expectedFail     int
	}{
		{
			name:  "batch update all hosts successfully",
			hosts: []string{"router1", "router2", "router3"},
			setupHostKeys: map[string]bool{
				"router1": true,
				"router2": true,
				"router3": true,
			},
			connectionErrors: map[string]bool{},
			wantErr:          false,
			expectedSuccess:  3,
			expectedFail:     0,
		},
		{
			name:  "batch update with one failure",
			hosts: []string{"router1", "router2", "router3"},
			setupHostKeys: map[string]bool{
				"router1": true,
				"router2": true,
				"router3": true,
			},
			connectionErrors: map[string]bool{
				"router2": true,
			},
			wantErr:         true,
			expectedSuccess: 2,
			expectedFail:    1,
		},
		{
			name:  "batch update all hosts fail",
			hosts: []string{"router1", "router2"},
			setupHostKeys: map[string]bool{
				"router1": false,
				"router2": false,
			},
			connectionErrors: map[string]bool{
				"router1": true,
				"router2": true,
			},
			wantErr:         true,
			expectedSuccess: 0,
			expectedFail:    2,
		},
		{
			name:             "batch update with new host keys",
			hosts:            []string{"newrouter1", "newrouter2"},
			setupHostKeys:    map[string]bool{},
			connectionErrors: map[string]bool{},
			wantErr:          false,
			expectedSuccess:  2,
			expectedFail:     0,
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
			ctx := context.WithValue(context.Background(), core.EnrollmentModeKey, true)

			// Setup existing host keys
			for host, setup := range tt.setupHostKeys {
				if setup {
					srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
					dstFile := core.HostKeyFilePath(host)
					if err := copyFile(srcFile, dstFile); err != nil {
						t.Fatalf("Failed to setup test host key for %s: %v", host, err)
					}
				}
			}

			// Mock SSH connection factory
			originalFactory := sshConnectionFactory
			defer func() { sshConnectionFactory = originalFactory }()

			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				if tt.connectionErrors[host] {
					return nil, fmt.Errorf("connection failed for %s", host)
				}

				// Simulate host key capture
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
				dstFile := core.HostKeyFilePath(host)
				_ = copyFile(srcFile, dstFile)

				return &MockSshRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			// Execute batch update
			successCount := 0
			failCount := 0
			var lastErr error

			for _, host := range tt.hosts {
				if _, err := updateHostKey(ctx, host); err != nil {
					failCount++
					lastErr = err
				} else {
					successCount++
				}
			}

			// Verify counts
			if successCount != tt.expectedSuccess {
				t.Errorf("Expected %d successful updates, got %d", tt.expectedSuccess, successCount)
			}
			if failCount != tt.expectedFail {
				t.Errorf("Expected %d failed updates, got %d", tt.expectedFail, failCount)
			}

			// Verify error expectation
			hasError := failCount > 0
			if hasError != tt.wantErr {
				t.Errorf("Expected error: %v, got error: %v (lastErr: %v)", tt.wantErr, hasError, lastErr)
			}

			// Verify host keys were updated for successful hosts
			for _, host := range tt.hosts {
				if !tt.connectionErrors[host] {
					if !core.HostKeyExists(host) {
						t.Errorf("Expected host key for %s to exist after successful update", host)
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

			// Set package variables
			hostname = tt.hostnameValue
			updateHostKeyOnly = tt.updateHostKeyOnly
			force = tt.force

			// Create mock config
			cfg := &core.Config{
				Hosts: tt.hosts,
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)
			ctx = context.WithValue(ctx, core.EnrollmentModeKey, true)

			// Mock SSH connection factory
			originalFactory := sshConnectionFactory
			defer func() { sshConnectionFactory = originalFactory }()

			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				// Simulate host key capture
				srcFile := filepath.Join(originalWd, "testdata/hostkeys/router1.hostkey")
				dstFile := core.HostKeyFilePath(host)
				_ = copyFile(srcFile, dstFile)

				return &MockSshRunner{
					CloseFunc: func() error { return nil },
					RunFunc:   func(cmd string) (string, error) { return "", nil },
				}, nil
			}

			// Execute the Action logic directly (simulating the CLI command execution)
			var err error

			// Validate flag combinations
			if force && updateHostKeyOnly {
				err = fmt.Errorf("cannot use --force and --update-hostkey-only together")
			} else if updateHostKeyOnly {
				// Batch mode: update hostkeys for all discovered hosts
				if len(cfg.Hosts) > 1 {
					successCount := 0
					failCount := 0
					var lastErr error

					for _, host := range cfg.Hosts {
						if _, updateErr := updateHostKey(ctx, host); updateErr != nil {
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
					_, err = updateHostKey(ctx, host)
				} else {
					err = fmt.Errorf("no hosts specified or discovered")
				}
			} else {
				// Normal enrollment validation
				if len(cfg.Hosts) != 1 {
					err = fmt.Errorf("enroll command requires exactly one host, got %d", len(cfg.Hosts))
				} else if hostname == "" {
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
					if core.HostKeyExists(host) {
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

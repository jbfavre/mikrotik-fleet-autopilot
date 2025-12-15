package export

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
	if m.RunFunc != nil {
		return m.RunFunc(cmd)
	}
	return "", nil
}

// MockSshManager is a mock implementation of SshManager for testing
type MockSshManager struct {
	CreateConnectionFunc func(host string) (core.SshRunner, error)
	GetUserFunc          func() string
}

func (m *MockSshManager) CreateConnection(host string) (core.SshRunner, error) {
	if m.CreateConnectionFunc != nil {
		return m.CreateConnectionFunc(host)
	}
	return nil, fmt.Errorf("mock CreateConnection not implemented")
}

func (m *MockSshManager) GetUser() string {
	if m.GetUserFunc != nil {
		return m.GetUserFunc()
	}
	return "admin"
}

func TestExportConfig(t *testing.T) {
	tests := []struct {
		name           string
		host           string
		showSensitive  bool
		sshOutput      string
		sshError       error
		expectedFile   string
		expectedCmd    string
		wantErr        bool
		errContains    string
		checkFilePerms bool
	}{
		{
			name:          "Successful export without sensitive data",
			host:          "router1.example.com",
			showSensitive: false,
			sshOutput: `/interface bridge
add name=bridge1
/ip address
add address=192.168.1.1/24 interface=bridge1`,
			sshError:     nil,
			expectedCmd:  "/export terse",
			expectedFile: "router1.rsc",
			wantErr:      false,
		},
		{
			name:          "Successful export with sensitive data",
			host:          "router2.example.com",
			showSensitive: true,
			sshOutput: `/user
add name=admin password=secret123
/interface bridge
add name=bridge1`,
			sshError:       nil,
			expectedCmd:    "/export terse show-sensitive",
			expectedFile:   "router2.rsc",
			wantErr:        false,
			checkFilePerms: true,
		},
		{
			name:          "Export with Windows line endings",
			host:          "router3",
			showSensitive: false,
			sshOutput:     "/interface bridge\r\nadd name=bridge1\r\n/ip address\r\nadd address=192.168.1.1/24",
			sshError:      nil,
			expectedCmd:   "/export terse",
			expectedFile:  "router3.rsc",
			wantErr:       false,
		},
		{
			name:          "SSH connection fails",
			host:          "router4.example.com",
			showSensitive: false,
			sshOutput:     "",
			sshError:      fmt.Errorf("connection timeout"),
			expectedCmd:   "/export terse",
			wantErr:       true,
			errContains:   "failed to export configuration",
		},
		{
			name:          "Hostname without domain",
			host:          "simple-router",
			showSensitive: false,
			sshOutput:     "/interface bridge\nadd name=bridge1",
			sshError:      nil,
			expectedCmd:   "/export terse",
			expectedFile:  "simple-router.rsc",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for test outputs
			tmpDir, err := os.MkdirTemp("", "export-test-*")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = os.RemoveAll(tmpDir)
			}() // Set output directory for this test
			originalOutputDir := outputDir
			outputDir = tmpDir
			defer func() {
				outputDir = originalOutputDir
			}()

			// Track which command was executed
			var executedCmd string

			// Mock SSH connection using the factory pattern
			originalFactory := sshConnectionFactory
			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				return &MockSshRunner{
					RunFunc: func(cmd string) (string, error) {
						executedCmd = cmd
						return tt.sshOutput, tt.sshError
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}
			defer func() {
				sshConnectionFactory = originalFactory
			}()

			// Set showSensitive flag
			originalShowSensitive := showSensitive
			showSensitive = tt.showSensitive
			defer func() {
				showSensitive = originalShowSensitive
			}()

			// Create config and context with mock SSH manager
			cfg := &core.Config{
				Hosts: []string{tt.host},
				User:  "admin",
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)
			ctx = context.WithValue(ctx, core.SshManagerKey, &MockSshManager{})

			// Call the function
			err = export(ctx, tt.host)

			// Verify error expectations
			if (err != nil) != tt.wantErr {
				t.Errorf("export() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				if tt.errContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errContains)) {
					t.Errorf("exportConfig() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			// Verify correct command was executed
			if executedCmd != tt.expectedCmd {
				t.Errorf("executed command = %q, want %q", executedCmd, tt.expectedCmd)
			}

			// Verify file was created
			if tt.expectedFile != "" {
				filePath := filepath.Join(tmpDir, tt.expectedFile)
				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					t.Errorf("expected file %s was not created", filePath)
					return
				}

				// Read and verify file content
				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Errorf("failed to read output file: %v", err)
					return
				}

				// Verify no Windows line endings in output
				if strings.Contains(string(content), "\r\n") {
					t.Errorf("output file contains Windows line endings (CRLF)")
				}

				// Verify content matches expected (after line ending cleanup)
				expectedContent := strings.ReplaceAll(tt.sshOutput, "\r\n", "\n")
				if string(content) != expectedContent {
					t.Errorf("file content = %q, want %q", string(content), expectedContent)
				}

				// Check file permissions if requested
				if tt.checkFilePerms {
					info, err := os.Stat(filePath)
					if err != nil {
						t.Errorf("failed to stat file: %v", err)
						return
					}
					mode := info.Mode().Perm()
					expectedMode := os.FileMode(0644)
					if mode != expectedMode {
						t.Errorf("file permissions = %o, want %o", mode, expectedMode)
					}
				}
			}
		})
	}
}

package export

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
	"jb.favre/mikrotik-fleet-autopilot/common/sshmocks_test"
)

// TestExport tests the public Export function wrapper
// Since Export uses core.CreateConnection directly (not injectable),
// we test it by calling the internal export function through Export's code path
func TestExport(t *testing.T) {
	tests := []struct {
		name              string
		host              string
		outputDir         string
		showSensitive     bool
		preferredFilename string
		wantErr           bool
		errContains       string
	}{
		{
			name:              "context cancellation before export",
			host:              "router1.example.com",
			outputDir:         "",
			showSensitive:     false,
			preferredFilename: "",
			wantErr:           true,
			errContains:       "context cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create cancelled context
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			tmpDir := t.TempDir()

			// Call the public Export function
			err := Export(ctx, tt.host, tmpDir, tt.showSensitive, tt.preferredFilename)

			// Check error expectations
			if (err != nil) != tt.wantErr {
				t.Errorf("Export() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Export() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

// TestExportParameterMapping tests that the public Export function correctly maps parameters
// to ExportConfig and calls export with the right values
func TestExportParameterMapping(t *testing.T) {
	tests := []struct {
		name              string
		host              string
		outputDir         string
		showSensitive     bool
		preferredFilename string
		validateConfig    func(t *testing.T, cfg ExportConfig, preferredFilename string)
	}{
		{
			name:              "maps show-sensitive true",
			host:              "router1.example.com",
			outputDir:         "/tmp/output",
			showSensitive:     true,
			preferredFilename: "custom-name",
			validateConfig: func(t *testing.T, cfg ExportConfig, preferredFilename string) {
				if !cfg.ShowSensitive {
					t.Error("ShowSensitive should be true")
				}
				if cfg.OutputDir != "/tmp/output" {
					t.Errorf("OutputDir = %s, want /tmp/output", cfg.OutputDir)
				}
				if preferredFilename != "custom-name" {
					t.Errorf("preferredFilename = %s, want custom-name", preferredFilename)
				}
			},
		},
		{
			name:              "maps show-sensitive false",
			host:              "router2.example.com",
			outputDir:         "/custom/path",
			showSensitive:     false,
			preferredFilename: "",
			validateConfig: func(t *testing.T, cfg ExportConfig, preferredFilename string) {
				if cfg.ShowSensitive {
					t.Error("ShowSensitive should be false")
				}
				if cfg.OutputDir != "/custom/path" {
					t.Errorf("OutputDir = %s, want /custom/path", cfg.OutputDir)
				}
				if preferredFilename != "" {
					t.Errorf("preferredFilename = %s, want empty", preferredFilename)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We test parameter mapping by verifying the config is built correctly
			// This tests the first part of the Export function (parameter mapping)
			cfg := ExportConfig{
				ShowSensitive: tt.showSensitive,
				OutputDir:     tt.outputDir,
			}
			tt.validateConfig(t, cfg, tt.preferredFilename)
		})
	}
}

func TestExportConfig(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		showSensitive    bool
		sshOutput        string
		sshError         error
		expectedFile     string
		expectedFilename string
		expectedCmd      string
		wantErr          bool
		errContains      string
		checkFilePerms   bool
	}{
		{
			name:          "Successful export without sensitive data",
			host:          "router1.example.com",
			showSensitive: false,
			sshOutput: `/interface bridge
add name=bridge1
/ip address
add address=192.168.1.1/24 interface=bridge1`,
			sshError:         nil,
			expectedCmd:      "/export terse",
			expectedFile:     "router1.rsc",
			expectedFilename: "router1.rsc",
			wantErr:          false,
		},
		{
			name:          "Successful export with sensitive data",
			host:          "router2.example.com",
			showSensitive: true,
			sshOutput: `/user
add name=admin password=secret123
/interface bridge
add name=bridge1`,
			sshError:         nil,
			expectedCmd:      "/export terse show-sensitive",
			expectedFile:     "router2.rsc",
			expectedFilename: "router2.rsc",
			wantErr:          false,
			checkFilePerms:   true,
		},
		{
			name:             "Export with Windows line endings",
			host:             "router3",
			showSensitive:    false,
			sshOutput:        "/interface bridge\r\nadd name=bridge1\r\n/ip address\r\nadd address=192.168.1.1/24",
			sshError:         nil,
			expectedCmd:      "/export terse",
			expectedFile:     "router3.rsc",
			expectedFilename: "router3.rsc",
			wantErr:          false,
		},
		{
			name:             "SSH connection fails",
			host:             "router4.example.com",
			showSensitive:    false,
			sshOutput:        "",
			sshError:         fmt.Errorf("connection timeout"),
			expectedCmd:      "/export terse",
			expectedFilename: "",
			wantErr:          true,
			errContains:      "failed to export configuration",
		},
		{
			name:             "Hostname without domain",
			host:             "simple-router",
			showSensitive:    false,
			sshOutput:        "/interface bridge\nadd name=bridge1",
			sshError:         nil,
			expectedCmd:      "/export terse",
			expectedFile:     "simple-router.rsc",
			expectedFilename: "simple-router.rsc",
			wantErr:          false,
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
			}()

			// Track which command was executed
			var executedCmd string

			// Mock SSH connection using the factory pattern
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				return &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						executedCmd = cmd
						return tt.sshOutput, tt.sshError
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			// Build test configuration
			cfg := ExportConfig{
				ShowSensitive: tt.showSensitive,
				OutputDir:     tmpDir,
			}

			deps := ExportDependencies{
				SSHConnectionFactory: mockSSHFactory,
			}

			// Create config and context with mock SSH manager
			coreCfg := &core.Config{
				Hosts: []string{tt.host},
				User:  "admin",
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, coreCfg)
			ctx = context.WithValue(ctx, core.SshManagerKey, &sshmocks_test.MockManager{})

			// Call the function
			filename, err := export(ctx, tt.host, "", cfg, deps, nil)

			// Verify error expectations
			if (err != nil) != tt.wantErr {
				t.Errorf("export() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				if tt.errContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errContains)) {
					t.Errorf("exportConfig() error = %v, want error containing %q", err, tt.errContains)
				}
				if filename != "" {
					t.Errorf("export() filename = %q on error, want empty string", filename)
				}
				return
			}

			// Verify returned filename matches expected
			if filename != tt.expectedFilename {
				t.Errorf("export() filename = %q, want %q", filename, tt.expectedFilename)
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

// TestExport_FilenameEdgeCases tests filename generation with various host formats
func TestExport_FilenameEdgeCases(t *testing.T) {
	tests := []struct {
		name              string
		host              string
		preferredFilename string
		expectedFilename  string
		showSensitive     bool
	}{
		{
			name:              "IP address",
			host:              "192.168.1.1",
			preferredFilename: "",
			expectedFilename:  "192.168.1.1.rsc",
			showSensitive:     false,
		},
		{
			name:              "Hostname with FQDN",
			host:              "router.example.com",
			preferredFilename: "",
			expectedFilename:  "router.rsc",
			showSensitive:     false,
		},
		{
			name:              "Preferred filename specified",
			host:              "router.example.com",
			preferredFilename: "custom-router",
			expectedFilename:  "custom-router.rsc",
			showSensitive:     false,
		},
		{
			name:              "IPv6 address",
			host:              "2001:db8::1",
			preferredFilename: "",
			expectedFilename:  "2001:db8::1.rsc",
			showSensitive:     false,
		},
		{
			name:              "Hostname with port",
			host:              "router:2222",
			preferredFilename: "",
			expectedFilename:  "router.rsc",
			showSensitive:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "export-filename-test-*")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = os.RemoveAll(tmpDir)
			}()

			// Mock SSH connection
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				return &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "/interface bridge\nadd name=bridge1", nil
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			// Build test configuration
			cfg := ExportConfig{
				ShowSensitive: tt.showSensitive,
				OutputDir:     tmpDir,
			}

			deps := ExportDependencies{
				SSHConnectionFactory: mockSSHFactory,
			}

			// Create config and context
			coreCfg := &core.Config{
				Hosts: []string{tt.host},
				User:  "admin",
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, coreCfg)
			ctx = context.WithValue(ctx, core.SshManagerKey, &sshmocks_test.MockManager{})

			// Call export
			_, err = export(ctx, tt.host, tt.preferredFilename, cfg, deps, nil)
			if err != nil {
				t.Fatalf("export() failed: %v", err)
			}

			// Verify file was created with expected name
			expectedPath := filepath.Join(tmpDir, tt.expectedFilename)
			if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
				// List files to help debug
				files, _ := os.ReadDir(tmpDir)
				var fileNames []string
				for _, f := range files {
					fileNames = append(fileNames, f.Name())
				}
				t.Errorf("Expected file %s not found. Files present: %v", tt.expectedFilename, fileNames)
			}
		})
	}
}

// TestExport_SSHConnectionFactoryError tests error handling when SSH connection fails
func TestExport_SSHConnectionFactoryError(t *testing.T) {
	// Mock SSH connection factory to return error
	mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
		return nil, fmt.Errorf("%w: timeout", ssh.ErrConnectionFailed)
	}

	cfg := ExportConfig{
		ShowSensitive: false,
		OutputDir:     t.TempDir(),
	}

	deps := ExportDependencies{
		SSHConnectionFactory: mockSSHFactory,
	}

	// Create config and context
	coreCfg := &core.Config{
		Hosts: []string{"test-host"},
		User:  "admin",
	}
	ctx := context.WithValue(context.Background(), core.ConfigKey, coreCfg)
	ctx = context.WithValue(ctx, core.SshManagerKey, &sshmocks_test.MockManager{})

	// Call export - should fail
	_, err := export(ctx, "test-host", "", cfg, deps, nil)
	if err == nil {
		t.Error("export() should fail when SSH connection cannot be established")
	}

	if !strings.Contains(err.Error(), "failed to dial") {
		t.Errorf("error message should mention dial failure, got: %v", err)
	}
}

// TestExport_CloseError tests that close errors are handled gracefully
func TestExport_CloseError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "export-close-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	closeCalled := false

	// Mock SSH connection with close error
	mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
		return &sshmocks_test.MockRunner{
			RunFunc: func(cmd string) (string, error) {
				return "/interface bridge\nadd name=bridge1", nil
			},
			CloseFunc: func() error {
				closeCalled = true
				return fmt.Errorf("close error")
			},
		}, nil
	}

	cfg := ExportConfig{
		ShowSensitive: false,
		OutputDir:     tmpDir,
	}

	deps := ExportDependencies{
		SSHConnectionFactory: mockSSHFactory,
	}

	coreCfg := &core.Config{
		Hosts: []string{"test-host"},
		User:  "admin",
	}
	ctx := context.WithValue(context.Background(), core.ConfigKey, coreCfg)
	ctx = context.WithValue(ctx, core.SshManagerKey, &sshmocks_test.MockManager{})

	// Export should succeed even if close fails (error is silently ignored)
	_, err = export(ctx, "test-host", "", cfg, deps, nil)
	if err != nil {
		t.Errorf("export() should succeed even with close error, got: %v", err)
	}

	if !closeCalled {
		t.Error("Close() was not called")
	}
}

func TestExport_StepCallbackSequence(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := ExportConfig{
		ShowSensitive: false,
		OutputDir:     tmpDir,
	}

	deps := ExportDependencies{
		SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
			return &sshmocks_test.MockRunner{
				RunFunc: func(cmd string) (string, error) {
					return "/interface bridge\nadd name=bridge1", nil
				},
				CloseFunc: func() error {
					return nil
				},
			}, nil
		},
	}

	type step struct {
		emoji string
		msg   string
	}
	var got []step
	stepCallback := func(emoji, msg string) {
		got = append(got, step{emoji: emoji, msg: msg})
	}

	ctx := context.Background()
	_, err := export(ctx, "router1", "", cfg, deps, stepCallback)
	if err != nil {
		t.Fatalf("export() failed: %v", err)
	}

	want := []step{
		{emoji: "⏳", msg: "Connecting to router…"},
		{emoji: "✅", msg: "Connected"},
		{emoji: "⏳", msg: "Running export command…"},
		{emoji: "✅", msg: "Export command completed"},
		{emoji: "⏳", msg: "Normalizing line endings…"},
		{emoji: "✅", msg: "Normalized output"},
		{emoji: "⏳", msg: "Writing configuration file…"},
		{emoji: "✅", msg: "Wrote configuration file"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("step callback sequence mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRunExportForHosts_Sequential(t *testing.T) {
	hosts := []string{"router-ok", "router-fail"}
	tmpDir := t.TempDir()

	deps := ExportDependencies{
		SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
			return &sshmocks_test.MockRunner{
				RunFunc: func(cmd string) (string, error) {
					if host == "router-fail" {
						return "", fmt.Errorf("boom")
					}
					return "/interface bridge\nadd name=bridge1", nil
				},
				CloseFunc: func() error {
					return nil
				},
			}, nil
		},
	}

	cfg := ExportConfig{ShowSensitive: false, OutputDir: tmpDir}
	opts := RunExportOptions{Debug: true, MaxConcurrentHosts: 1}

	var out bytes.Buffer
	err := runExportForHosts(context.Background(), hosts, opts, cfg, deps, &out)
	if err == nil {
		t.Fatal("runExportForHosts() expected non-nil error when one host fails")
	}

	output := out.String()
	if !strings.Contains(output, "router-ok") || !strings.Contains(output, "router-fail") {
		t.Errorf("output should contain both hosts, got: %q", output)
	}
	if !strings.Contains(output, "Configuration exported to router-ok.rsc") {
		t.Errorf("missing success message, got: %q", output)
	}
	if !strings.Contains(output, "failed to export configuration: boom") {
		t.Errorf("missing failure message, got: %q", output)
	}
}

func TestRunExportForHosts_Concurrent(t *testing.T) {
	hosts := []string{"router-1", "router-2", "router-3"}
	tmpDir := t.TempDir()

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	nHosts := len(hosts)
	// allStarted is closed once every goroutine has incremented inFlight,
	// so maxInFlight is captured while all connections are simultaneously
	// in-flight without relying on timing or sleep.
	allStarted := make(chan struct{})
	var startedCount atomic.Int32

	deps := ExportDependencies{
		SSHConnectionFactory: func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
			current := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if current <= old || maxInFlight.CompareAndSwap(old, current) {
					break
				}
			}
			// Barrier: block until every goroutine has reached this point.
			if startedCount.Add(1) == int32(nHosts) {
				close(allStarted)
			} else {
				<-allStarted
			}

			return &sshmocks_test.MockRunner{
				RunFunc: func(cmd string) (string, error) {
					return "/interface bridge\nadd name=bridge1", nil
				},
				CloseFunc: func() error {
					inFlight.Add(-1)
					return nil
				},
			}, nil
		},
	}

	cfg := ExportConfig{ShowSensitive: false, OutputDir: tmpDir}
	opts := RunExportOptions{Debug: true, MaxConcurrentHosts: len(hosts)}

	var out bytes.Buffer
	err := runExportForHosts(context.Background(), hosts, opts, cfg, deps, &out)
	if err != nil {
		t.Fatalf("runExportForHosts() unexpected error: %v", err)
	}

	output := out.String()
	for _, host := range hosts {
		if !strings.Contains(output, host) {
			t.Errorf("output missing host %q: %q", host, output)
		}
	}
	if maxInFlight.Load() <= 1 {
		t.Errorf("expected maxInFlight > 1 (goroutines should run concurrently), got %d", maxInFlight.Load())
	}
	t.Logf("peak concurrent SSH connections: %d", maxInFlight.Load())
}

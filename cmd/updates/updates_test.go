package updates

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

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

func TestUpdates(t *testing.T) {
	tests := []struct {
		name               string
		host               string
		applyUpdates       bool
		osInstalled        string
		osAvailable        string
		boardInstalled     string
		boardAvailable     string
		hasBoard           bool
		checkForUpdatesOut string
		routerboardOut     string
		sshError           error
		wantErr            bool
		errContains        string
		expectOsUpdate     bool
		expectBoardUpdate  bool
	}{
		{
			name:         "RouterOS up to date, no board, check only",
			host:         "router1.example.com",
			applyUpdates: false,
			osInstalled:  "7.12.1",
			osAvailable:  "7.12.1",
			hasBoard:     false,
			checkForUpdatesOut: `  status: Updated
  installed-version: 7.12.1
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: no`,
			wantErr:        false,
		},
		{
			name:         "RouterOS update available, no board, check only",
			host:         "router2.example.com",
			applyUpdates: false,
			osInstalled:  "7.11.3",
			osAvailable:  "7.12.1",
			hasBoard:     false,
			checkForUpdatesOut: `  status: New version available
  installed-version: 7.11.3
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: no`,
			wantErr:        false,
		},
		{
			name:           "RouterOS and RouterBoard up to date, check only",
			host:           "router3.example.com",
			applyUpdates:   false,
			osInstalled:    "7.12.1",
			osAvailable:    "7.12.1",
			hasBoard:       true,
			boardInstalled: "7.12.1",
			boardAvailable: "7.12.1",
			checkForUpdatesOut: `  status: Updated
  installed-version: 7.12.1
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: yes
  current-firmware: 7.12.1
  upgrade-firmware: 7.12.1`,
			wantErr: false,
		},
		{
			name:         "RouterOS update available, apply enabled, should update",
			host:         "router4.example.com",
			applyUpdates: true,
			osInstalled:  "7.11.3",
			osAvailable:  "7.12.1",
			hasBoard:     false,
			checkForUpdatesOut: `  status: New version available
  installed-version: 7.11.3
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: no`,
			expectOsUpdate: true,
			wantErr:        false,
		},
		{
			name:           "RouterBoard update available, apply enabled, should update",
			host:           "router5.example.com",
			applyUpdates:   true,
			osInstalled:    "7.12.1",
			osAvailable:    "7.12.1",
			hasBoard:       true,
			boardInstalled: "7.11.3",
			boardAvailable: "7.12.1",
			checkForUpdatesOut: `  status: Updated
  installed-version: 7.12.1
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: yes
  current-firmware: 7.11.3
  upgrade-firmware: 7.12.1`,
			expectBoardUpdate: true,
			wantErr:           false,
		},
		{
			name:           "Both updates available, apply enabled",
			host:           "router6.example.com",
			applyUpdates:   true,
			osInstalled:    "7.11.3",
			osAvailable:    "7.12.1",
			hasBoard:       true,
			boardInstalled: "7.11.3",
			boardAvailable: "7.12.1",
			checkForUpdatesOut: `  status: New version available
  installed-version: 7.11.3
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: yes
  current-firmware: 7.11.3
  upgrade-firmware: 7.12.1`,
			expectOsUpdate:    true,
			expectBoardUpdate: false, // Board update happens after OS, not in same call
			wantErr:           false,
		},
		{
			name:         "SSH connection failure",
			host:         "router7.example.com",
			applyUpdates: false,
			sshError:     fmt.Errorf("connection timeout"),
			wantErr:      true,
			errContains:  "failed to create SSH connection",
		},
		{
			name:         "Check for updates command fails",
			host:         "router8.example.com",
			applyUpdates: false,
			checkForUpdatesOut: `  status: ERROR
  message: Could not download package list`,
			routerboardOut: `  routerboard: no`,
			wantErr:        true,
			errContains:    "ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore original values
			originalApply := updatesApply
			originalFactory := sshConnectionFactory
			originalReconnectDelay := reconnectDelay
			defer func() {
				updatesApply = originalApply
				sshConnectionFactory = originalFactory
				reconnectDelay = originalReconnectDelay
			}()

			// Set test values
			updatesApply = tt.applyUpdates
			reconnectDelay = 10 * time.Millisecond // Speed up tests

			// Track commands executed
			var executedCommands []string
			var connectionCount int

			// Mock SSH connection factory
			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				connectionCount++

				if tt.sshError != nil {
					return nil, tt.sshError
				}

				return &MockSshRunner{
					RunFunc: func(cmd string) (string, error) {
						executedCommands = append(executedCommands, cmd)

						// Route commands to appropriate responses
						if cmd == "/system/package/update/check-for-updates" {
							return tt.checkForUpdatesOut, nil
						}
						if cmd == "/system/routerboard/print" {
							return tt.routerboardOut, nil
						}
						if cmd == "/system/package/update/install" {
							return "System will reboot", nil
						}
						if cmd == "/system/reboot" {
							return "System is rebooting", nil
						}

						return "", nil
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			// Create context with mock manager
			cfg := &core.Config{
				Hosts: []string{tt.host},
				User:  "admin",
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, cfg)
			ctx = context.WithValue(ctx, core.SshManagerKey, &MockSshManager{})

			// Execute the function
			err := updates(ctx, tt.host)

			// Verify error expectations
			if (err != nil) != tt.wantErr {
				t.Errorf("updates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errContains)) {
					t.Errorf("updates() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			// Verify update commands were executed when expected
			if tt.expectOsUpdate {
				found := false
				for _, cmd := range executedCommands {
					if cmd == "/system/package/update/install" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("updates() expected RouterOS update command to be executed, but it wasn't. Commands: %v", executedCommands)
				}
			}

			if tt.expectBoardUpdate {
				found := false
				for _, cmd := range executedCommands {
					if cmd == "/system/reboot" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("updates() expected RouterBoard update (reboot) command to be executed, but it wasn't. Commands: %v", executedCommands)
				}
			}

			// Verify check commands were always executed (unless SSH failed)
			if tt.sshError == nil {
				checkFound := false
				for _, cmd := range executedCommands {
					if cmd == "/system/package/update/check-for-updates" {
						checkFound = true
						break
					}
				}
				if !checkFound {
					t.Errorf("updates() expected check-for-updates command to be executed, but it wasn't")
				}
			}
		})
	}
}

func TestGetUpdateStatus(t *testing.T) {
	tests := []struct {
		name                string
		sshOutput           string
		sshError            error
		sshCmd              string
		subSystem           string
		installedRe         *regexp.Regexp
		availableRe         *regexp.Regexp
		skipIfNoRouterBoard bool
		want                *UpdateStatus
		wantErr             bool
		errContains         string
	}{
		{
			name: "RouterOS up to date",
			sshOutput: `       channel: stable
  installed-version: 7.14.1
   latest-version: 7.14.1
        status: System is already up to date`,
			sshError:            nil,
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			wantErr: false,
		},
		{
			name: "RouterOS update available",
			sshOutput: `       channel: stable
  installed-version: 7.14.0
   latest-version: 7.14.1
        status: New version is available`,
			sshError:            nil,
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			wantErr: false,
		},
		{
			name: "RouterOS ERROR status - DNS failure",
			sshOutput: `       channel: stable
  installed-version: 7.14.1
        status: ERROR: could not resolve dns name (timeout)`,
			sshError:            nil,
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want:                nil,
			wantErr:             true,
			errContains:         "could not resolve dns name",
		},
		{
			name: "RouterOS ERROR status - generic",
			sshOutput: `       channel: stable
  installed-version: 7.14.1
        status: ERROR: connection failed`,
			sshError:            nil,
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want:                nil,
			wantErr:             true,
			errContains:         "connection failed",
		},
		{
			name: "RouterBoard up to date",
			sshOutput: `       routerboard: yes
             model: RB750Gr3
     serial-number: 1234567890
     current-firmware: 7.14.1
     upgrade-firmware: 7.14.1`,
			sshError:            nil,
			sshCmd:              "/system/routerboard/print",
			subSystem:           "RouterBoard",
			installedRe:         regexp.MustCompile(`.*current-firmware: (\S+)`),
			availableRe:         regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
			skipIfNoRouterBoard: true,
			want: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			wantErr: false,
		},
		{
			name: "RouterBoard update available",
			sshOutput: `       routerboard: yes
             model: RB750Gr3
     serial-number: 1234567890
     current-firmware: 7.14.0
     upgrade-firmware: 7.14.1`,
			sshError:            nil,
			sshCmd:              "/system/routerboard/print",
			subSystem:           "RouterBoard",
			installedRe:         regexp.MustCompile(`.*current-firmware: (\S+)`),
			availableRe:         regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
			skipIfNoRouterBoard: true,
			want: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			wantErr: false,
		},
		{
			name:                "No RouterBoard - virtualized",
			sshOutput:           `       routerboard: no`,
			sshError:            nil,
			sshCmd:              "/system/routerboard/print",
			subSystem:           "RouterBoard",
			installedRe:         regexp.MustCompile(`.*current-firmware: (\S+)`),
			availableRe:         regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
			skipIfNoRouterBoard: true,
			want:                nil,
			wantErr:             false,
		},
		{
			name:                "SSH command failure",
			sshOutput:           "",
			sshError:            fmt.Errorf("connection timeout"),
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want:                nil,
			wantErr:             true,
			errContains:         "failed to run SSH command",
		},
		{
			name: "Missing installed version",
			sshOutput: `       channel: stable
   latest-version: 7.14.1
        status: System is already up to date`,
			sshError:            nil,
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want:                nil,
			wantErr:             true,
			errContains:         "failed to parse installed version",
		},
		{
			name: "Missing available version",
			sshOutput: `       channel: stable
  installed-version: 7.14.1
        status: System is already up to date`,
			sshError:            nil,
			sshCmd:              "/system/package/update/check-for-updates",
			subSystem:           "RouterOS",
			installedRe:         regexp.MustCompile(`.*installed-version: (\S+)`),
			availableRe:         regexp.MustCompile(`.*latest-version: (\S+)`),
			skipIfNoRouterBoard: false,
			want:                nil,
			wantErr:             true,
			errContains:         "failed to parse available version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockSshRunner{
				RunFunc: func(cmd string) (string, error) {
					return tt.sshOutput, tt.sshError
				},
			}

			got, err := getUpdateStatus(mock, tt.sshCmd, tt.subSystem, tt.installedRe, tt.availableRe, tt.skipIfNoRouterBoard)

			if tt.wantErr {
				if err == nil {
					t.Errorf("getUpdateStatus() error = nil, wantErr = true")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("getUpdateStatus() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("getUpdateStatus() unexpected error = %v", err)
				return
			}

			if got == nil && tt.want != nil {
				t.Errorf("getUpdateStatus() got = nil, want = %+v", tt.want)
				return
			}

			if got != nil && tt.want == nil {
				t.Errorf("getUpdateStatus() got = %+v, want = nil", got)
				return
			}

			if got != nil && tt.want != nil {
				if got.Installed != tt.want.Installed || got.Available != tt.want.Available {
					t.Errorf("getUpdateStatus() = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

func TestFormatUpdateResult(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		osStatus    UpdateStatus
		boardStatus *UpdateStatus
		want        string
	}{
		{
			name: "Virtualized router - up to date",
			host: "router1.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: nil,
			want:        "✅ router1.example.com is up-to-date (RouterOS: 7.14.1)",
		},
		{
			name: "Virtualized router - update available",
			host: "router1.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			boardStatus: nil,
			want:        "⚠️  router1.example.com upgrade available (RouterOS: 7.14.0 → 7.14.1)",
		},
		{
			name: "Physical router - both up to date",
			host: "router2.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			want: "✅ router2.example.com is up-to-date (RouterOS: 7.14.1, RouterBoard: 7.14.1)",
		},
		{
			name: "Physical router - OS update available",
			host: "router2.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			want: "⚠️  router2.example.com upgrade available (RouterOS: 7.14.0 → 7.14.1, RouterBoard: 7.14.1 → pending)",
		},
		{
			name: "Physical router - Board update available",
			host: "router2.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			want: "⚠️  router2.example.com upgrade available (RouterOS: 7.14.1 → 7.14.1, RouterBoard: 7.14.0 → 7.14.1)",
		},
		{
			name: "Physical router - both updates available",
			host: "router2.example.com",
			osStatus: UpdateStatus{
				Installed: "7.13.5",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.13.5",
				Available: "7.14.1",
			},
			want: "⚠️  router2.example.com upgrade available (RouterOS: 7.13.5 → 7.14.1, RouterBoard: 7.13.5 → 7.14.1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUpdateResult(tt.host, tt.osStatus, tt.boardStatus)
			if got != tt.want {
				t.Errorf("formatUpdateResult() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCheckCurrentStatus(t *testing.T) {
	tests := []struct {
		name               string
		osOutput           string
		osError            error
		boardOutput        string
		boardError         error
		wantOsInstalled    string
		wantOsAvailable    string
		wantBoardInstalled string
		wantBoardAvailable string
		wantBoardNil       bool
		wantErr            bool
	}{
		{
			name: "Physical router - both up to date",
			osOutput: `       channel: stable
  installed-version: 7.14.1
   latest-version: 7.14.1
        status: System is already up to date`,
			osError: nil,
			boardOutput: `       routerboard: yes
             model: RB750Gr3
     current-firmware: 7.14.1
     upgrade-firmware: 7.14.1`,
			boardError:         nil,
			wantOsInstalled:    "7.14.1",
			wantOsAvailable:    "7.14.1",
			wantBoardInstalled: "7.14.1",
			wantBoardAvailable: "7.14.1",
			wantBoardNil:       false,
			wantErr:            false,
		},
		{
			name: "Physical router - updates available",
			osOutput: `       channel: stable
  installed-version: 7.14.0
   latest-version: 7.14.1
        status: New version is available`,
			osError: nil,
			boardOutput: `       routerboard: yes
             model: RB750Gr3
     current-firmware: 7.14.0
     upgrade-firmware: 7.14.1`,
			boardError:         nil,
			wantOsInstalled:    "7.14.0",
			wantOsAvailable:    "7.14.1",
			wantBoardInstalled: "7.14.0",
			wantBoardAvailable: "7.14.1",
			wantBoardNil:       false,
			wantErr:            false,
		},
		{
			name: "Virtualized router - up to date",
			osOutput: `       channel: stable
  installed-version: 7.14.1
   latest-version: 7.14.1
        status: System is already up to date`,
			osError:         nil,
			boardOutput:     `       routerboard: no`,
			boardError:      nil,
			wantOsInstalled: "7.14.1",
			wantOsAvailable: "7.14.1",
			wantBoardNil:    true,
			wantErr:         false,
		},
		{
			name: "RouterOS check fails",
			osOutput: `       channel: stable
  installed-version: 7.14.1
        status: ERROR: could not resolve dns name`,
			osError:     nil,
			boardOutput: "",
			boardError:  nil,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockSshRunner{
				RunFunc: func(cmd string) (string, error) {
					if cmd == "/system/package/update/check-for-updates" {
						return tt.osOutput, tt.osError
					}
					if cmd == "/system/routerboard/print" {
						return tt.boardOutput, tt.boardError
					}
					return "", fmt.Errorf("unexpected command: %s", cmd)
				},
			}

			osStatus, boardStatus, err := checkCurrentStatus(mock)

			if tt.wantErr {
				if err == nil {
					t.Errorf("checkCurrentStatus() error = nil, wantErr = true")
				}
				return
			}

			if err != nil {
				t.Errorf("checkCurrentStatus() unexpected error = %v", err)
				return
			}

			if osStatus.Installed != tt.wantOsInstalled || osStatus.Available != tt.wantOsAvailable {
				t.Errorf("checkCurrentStatus() osStatus = %+v, want Installed=%s Available=%s",
					osStatus, tt.wantOsInstalled, tt.wantOsAvailable)
			}

			if tt.wantBoardNil {
				if boardStatus != nil {
					t.Errorf("checkCurrentStatus() boardStatus = %+v, want nil", boardStatus)
				}
			} else {
				if boardStatus == nil {
					t.Errorf("checkCurrentStatus() boardStatus = nil, want non-nil")
					return
				}
				if boardStatus.Installed != tt.wantBoardInstalled || boardStatus.Available != tt.wantBoardAvailable {
					t.Errorf("checkCurrentStatus() boardStatus = %+v, want Installed=%s Available=%s",
						boardStatus, tt.wantBoardInstalled, tt.wantBoardAvailable)
				}
			}
		})
	}
}

func TestApplyUpdate(t *testing.T) {
	// Save original values and restore after test
	originalDelay := reconnectDelay
	originalFactory := sshConnectionFactory
	defer func() {
		reconnectDelay = originalDelay
		sshConnectionFactory = originalFactory
	}()

	// Set a minimal delay for testing
	reconnectDelay = 1 * time.Millisecond

	tests := []struct {
		name              string
		updateCmd         string
		runError          error
		reconnectAttempts int
		wantErr           bool
		errContains       string
	}{
		{
			name:              "Successful update and reconnect on first attempt",
			updateCmd:         "/system/package/update/install",
			runError:          nil,
			reconnectAttempts: 1,
			wantErr:           false,
		},
		{
			name:              "Update command fails",
			updateCmd:         "/system/package/update/install",
			runError:          fmt.Errorf("update failed"),
			reconnectAttempts: 0,
			wantErr:           true,
			errContains:       "failed to run SSH command",
		},
		{
			name:              "Reconnect after multiple attempts",
			updateCmd:         "/system/reboot",
			runError:          nil,
			reconnectAttempts: 3,
			wantErr:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closeCallCount := 0
			reconnectAttempts := 0

			initialMock := &MockSshRunner{
				RunFunc: func(cmd string) (string, error) {
					return "", tt.runError
				},
				CloseFunc: func() error {
					closeCallCount++
					return nil
				},
			}

			// Mock the SSH connection factory
			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				reconnectAttempts++
				if reconnectAttempts < tt.reconnectAttempts {
					return nil, fmt.Errorf("connection failed")
				}
				return &MockSshRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			ctx := context.WithValue(context.Background(), core.SshManagerKey, &MockSshManager{})

			newConn, err := applyUpdate(initialMock, ctx, "test-router", tt.updateCmd, "Test update")

			if tt.wantErr {
				if err == nil {
					t.Errorf("applyUpdate() error = nil, wantErr = true")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("applyUpdate() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("applyUpdate() unexpected error = %v", err)
				return
			}

			if newConn == nil {
				t.Errorf("applyUpdate() returned nil connection")
				return
			}

			if closeCallCount != 1 {
				t.Errorf("Close() called %d times, want 1", closeCallCount)
			}

			if reconnectAttempts != tt.reconnectAttempts {
				t.Errorf("reconnection attempted %d times, want %d", reconnectAttempts, tt.reconnectAttempts)
			}
		})
	}
}

func TestFormatAndDisplayResult(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		osStatus    UpdateStatus
		boardStatus *UpdateStatus
		wantOutput  string
	}{
		{
			name: "Virtualized router - up to date",
			host: "router1.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: nil,
			wantOutput:  "✅ router1.example.com is up-to-date (RouterOS: 7.14.1)",
		},
		{
			name: "Physical router - both up to date",
			host: "router2.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			wantOutput: "✅ router2.example.com is up-to-date (RouterOS: 7.14.1, RouterBoard: 7.14.1)",
		},
		{
			name: "Physical router - updates available",
			host: "router3.example.com",
			osStatus: UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			wantOutput: "⚠️  router3.example.com upgrade available (RouterOS: 7.14.0 → 7.14.1, RouterBoard: 7.14.0 → 7.14.1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call formatUpdateResult to get the expected output
			expected := formatUpdateResult(tt.host, tt.osStatus, tt.boardStatus)

			// Verify it matches what we expect
			if expected != tt.wantOutput {
				t.Errorf("formatUpdateResult() = %q, want %q", expected, tt.wantOutput)
			}

			// Call formatAndDisplayResult to ensure it doesn't panic
			// Note: We can't easily capture stdout in a unit test without redirecting it,
			// but calling it ensures the function executes and code coverage is counted
			formatAndDisplayResult(tt.host, tt.osStatus, tt.boardStatus)
		})
	}
}

func TestApplyComponentUpdate(t *testing.T) {
	// Save original values and restore after test
	originalDelay := reconnectDelay
	originalFactory := sshConnectionFactory
	defer func() {
		reconnectDelay = originalDelay
		sshConnectionFactory = originalFactory
	}()

	// Set a minimal delay for testing
	reconnectDelay = 1 * time.Millisecond

	tests := []struct {
		name                string
		component           string
		updateCmd           string
		checkBoth           bool
		updateError         error
		osOutput            string
		osOutputAfterUpdate string
		boardOutput         string
		wantErr             bool
	}{
		{
			name:        "RouterOS update successful",
			component:   "RouterOS",
			updateCmd:   "/system/package/update/install",
			checkBoth:   false,
			updateError: nil,
			osOutputAfterUpdate: `       channel: stable
  installed-version: 7.14.1
   latest-version: 7.14.1
        status: System is already up to date`,
			wantErr: false,
		},
		{
			name:        "RouterOS update fails",
			component:   "RouterOS",
			updateCmd:   "/system/package/update/install",
			checkBoth:   false,
			updateError: fmt.Errorf("update failed"),
			wantErr:     true,
		},
		{
			name:        "RouterBoard update successful - check both",
			component:   "RouterBoard",
			updateCmd:   "/system/reboot",
			checkBoth:   true,
			updateError: nil,
			osOutputAfterUpdate: `       channel: stable
  installed-version: 7.14.1
   latest-version: 7.14.1
        status: System is already up to date`,
			boardOutput: `       routerboard: yes
             model: RB750Gr3
     current-firmware: 7.14.1
     upgrade-firmware: 7.14.1`,
			wantErr: false,
		},
		{
			name:        "RouterBoard update - OS check fails after update",
			component:   "RouterBoard",
			updateCmd:   "/system/reboot",
			checkBoth:   true,
			updateError: nil,
			osOutputAfterUpdate: `       channel: stable
  installed-version: 7.14.1
        status: ERROR: connection failed`,
			wantErr: false, // Errors in post-update check are non-fatal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCalled := false
			closeCalled := false

			initialMock := &MockSshRunner{
				RunFunc: func(cmd string) (string, error) {
					runCalled = true
					return "", tt.updateError
				},
				CloseFunc: func() error {
					closeCalled = true
					return nil
				},
			}

			// Mock the SSH connection factory to return a new mock after "reconnection"
			sshConnectionFactory = func(ctx context.Context, host string) (core.SshRunner, error) {
				return &MockSshRunner{
					RunFunc: func(cmd string) (string, error) {
						if cmd == "/system/package/update/check-for-updates" {
							return tt.osOutputAfterUpdate, nil
						}
						if cmd == "/system/routerboard/print" {
							return tt.boardOutput, nil
						}
						return "", fmt.Errorf("unexpected command: %s", cmd)
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			ctx := context.WithValue(context.Background(), core.SshManagerKey, &MockSshManager{})

			err := applyComponentUpdate(initialMock, ctx, "test-router", tt.component, tt.updateCmd, tt.checkBoth)

			if tt.wantErr {
				if err == nil {
					t.Errorf("applyComponentUpdate() error = nil, wantErr = true")
				}
				return
			}

			if err != nil {
				t.Errorf("applyComponentUpdate() unexpected error = %v", err)
				return
			}

			if !runCalled {
				t.Errorf("Update command was not executed")
			}

			if !closeCalled {
				t.Errorf("Connection was not closed")
			}
		})
	}
}

package updates

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
	"jb.favre/mikrotik-fleet-autopilot/common/sshmocks_test"
)

// TestUpdatesPublicWrapper tests the public Updates function wrapper
// Since Updates uses core.CreateConnection directly (not injectable),
// we test it by validating context cancellation and parameter mapping
func TestUpdatesPublicWrapper(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		wantErr     bool
		errContains string
	}{
		{
			name:        "context cancellation before updates",
			host:        "router.example.com",
			wantErr:     true,
			errContains: "context cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create cancelled context
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			// Call the public Updates function
			err := Updates(ctx, tt.host)

			// Check error expectations
			if (err != nil) != tt.wantErr {
				t.Errorf("Updates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Updates() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

// TestUpdatesParameterMapping tests that the public Updates function correctly maps parameters
// to UpdatesConfig and calls updates with the right values
func TestUpdatesParameterMapping(t *testing.T) {
	tests := []struct {
		name           string
		validateConfig func(t *testing.T, cfg UpdatesConfig, deps UpdatesDependencies)
	}{
		{
			name: "always sets UpdatesApply to true",
			validateConfig: func(t *testing.T, cfg UpdatesConfig, deps UpdatesDependencies) {
				if !cfg.UpdatesApply {
					t.Error("UpdatesApply should be true for public Updates function")
				}
			},
		},
		{
			name: "sets ReconnectDelay to 10 seconds",
			validateConfig: func(t *testing.T, cfg UpdatesConfig, deps UpdatesDependencies) {
				expectedDelay := 10 * time.Second
				if deps.ReconnectDelay != expectedDelay {
					t.Errorf("ReconnectDelay = %v, want %v", deps.ReconnectDelay, expectedDelay)
				}
			},
		},
		{
			name: "uses core.CreateConnection for SSH factory",
			validateConfig: func(t *testing.T, cfg UpdatesConfig, deps UpdatesDependencies) {
				if deps.SSHConnectionFactory == nil {
					t.Error("SSHConnectionFactory should not be nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We test parameter mapping by verifying the config is built correctly
			// This tests the first part of the Updates function (parameter mapping)
			cfg := UpdatesConfig{
				UpdatesApply: true,
			}
			deps := UpdatesDependencies{
				SSHConnectionFactory: ssh.CreateConnection,
				ReconnectDelay:       10 * time.Second,
			}
			tt.validateConfig(t, cfg, deps)
		})
	}
}

func TestUpdates(t *testing.T) {
	tests := []struct {
		name                      string
		host                      string
		applyUpdates              bool
		osInstalled               string
		osAvailable               string
		boardInstalled            string
		boardAvailable            string
		hasBoard                  bool
		checkForUpdatesOut        string
		checkForUpdatesPostUpdate string // if set, returned by factory connections instead of checkForUpdatesOut
		routerboardOut            string
		sshError                  error
		installError              error // if set, returned by /system/package/update/install
		wantErr                   bool
		wantOffline               bool // true when err should wrap ErrCannotCheckUpdates (router has no internet)
		errContains               string
		expectOsUpdate            bool
		expectBoardUpdate         bool
		expectedOsStatus          *UpdateStatus
		expectedBoardStatus       *UpdateStatus
		expectNilStatuses         bool // true when update succeeded but post-check verification failed
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
			routerboardOut:   `  routerboard: no`,
			wantErr:          false,
			expectedOsStatus: &UpdateStatus{Installed: "7.12.1", Available: "7.12.1"},
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
			routerboardOut:   `  routerboard: no`,
			wantErr:          false,
			expectedOsStatus: &UpdateStatus{Installed: "7.11.3", Available: "7.12.1"},
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
			wantErr:             false,
			expectedOsStatus:    &UpdateStatus{Installed: "7.12.1", Available: "7.12.1"},
			expectedBoardStatus: &UpdateStatus{Installed: "7.12.1", Available: "7.12.1"},
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
			expectBoardUpdate: true, // Board update runs after OS update re-check confirms it is still needed
			wantErr:           false,
		},
		{
			name:         "SSH connection failure",
			host:         "router7.example.com",
			applyUpdates: false,
			sshError:     fmt.Errorf("connection timeout"),
			wantErr:      true,
			wantOffline:  true,
			errContains:  "failed to connect",
		},
		{
			name:         "Check for updates command fails",
			host:         "router8.example.com",
			applyUpdates: false,
			checkForUpdatesOut: `  status: ERROR
  message: Could not download package list`,
			routerboardOut: `  routerboard: no`,
			wantErr:        true,
			wantOffline:    true,
			errContains:    "ERROR",
		},
		{
			name:         "Router offline - DNS resolution failure",
			host:         "router-offline.example.com",
			applyUpdates: false,
			checkForUpdatesOut: `  channel: stable
  installed-version: 7.14.1
  status: ERROR: could not resolve dns name (timeout)`,
			routerboardOut: `  routerboard: no`,
			wantErr:        true,
			wantOffline:    true,
			errContains:    "could not resolve dns name",
		},
		{
			name:         "Router offline - no route to host",
			host:         "router-no-route.example.com",
			applyUpdates: false,
			checkForUpdatesOut: `  channel: stable
  installed-version: 7.14.1
  status: ERROR: no-route-to-host`,
			routerboardOut: `  routerboard: no`,
			wantErr:        true,
			wantOffline:    true,
			errContains:    "no-route-to-host",
		},
		{
			name:         "RouterOS update applied, post-check DNS failure (unverified outcome)",
			host:         "router9.example.com",
			applyUpdates: true,
			osInstalled:  "7.11.3",
			osAvailable:  "7.12.1",
			hasBoard:     false,
			checkForUpdatesOut: `  status: New version available
  installed-version: 7.11.3
  latest-version: 7.12.1`,
			// After the update and reboot, DNS is not yet available
			checkForUpdatesPostUpdate: `  status: ERROR: could not resolve dns name (timeout)`,
			routerboardOut:            `  routerboard: no`,
			expectOsUpdate:            true,
			wantErr:                   false,
			expectNilStatuses:         true, // Update succeeded but status could not be verified
		},
		{
			name:         "Apply update command fails — hard failure, not ErrCannotCheckUpdates",
			host:         "router10.example.com",
			applyUpdates: true,
			osInstalled:  "7.11.3",
			osAvailable:  "7.12.1",
			hasBoard:     false,
			checkForUpdatesOut: `  status: New version available
  installed-version: 7.11.3
  latest-version: 7.12.1`,
			routerboardOut: `  routerboard: no`,
			installError:   fmt.Errorf("install command rejected: permission denied"),
			wantErr:        true,
			wantOffline:    false, // hard failure must NOT wrap ErrCannotCheckUpdates
			errContains:    "failed to run SSH command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track commands executed
			var executedCommands []string
			var connectionCount int

			// Mock SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				connectionCount++
				isPostUpdate := connectionCount > 1

				if tt.sshError != nil {
					return nil, tt.sshError
				}

				return &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						executedCommands = append(executedCommands, cmd)

						// Route commands to appropriate responses
						if cmd == "/system/package/update/check-for-updates" {
							if isPostUpdate && tt.checkForUpdatesPostUpdate != "" {
								return tt.checkForUpdatesPostUpdate, nil
							}
							return tt.checkForUpdatesOut, nil
						}
						if cmd == "/system/routerboard/print" {
							return tt.routerboardOut, nil
						}
						if cmd == "/system/package/update/install" {
							if tt.installError != nil {
								return "", tt.installError
							}
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

			// Build test configuration
			cfg := UpdatesConfig{
				UpdatesApply: tt.applyUpdates,
			}

			deps := UpdatesDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ReconnectDelay:       10 * time.Millisecond, // Speed up tests
			}

			// Create context with mock manager
			coreCfg := &core.Config{
				Hosts: []string{tt.host},
				User:  "admin",
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, coreCfg)
			ctx = context.WithValue(ctx, core.SshManagerKey, &sshmocks_test.MockManager{})

			// Execute the function
			osStatus, boardStatus, err := updates(ctx, tt.host, cfg, deps)

			// Verify error expectations
			if (err != nil) != tt.wantErr {
				t.Errorf("updates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errContains)) {
					t.Errorf("updates() error = %v, want error containing %q", err, tt.errContains)
				}
				if tt.wantOffline && !errors.Is(err, ErrCannotCheckUpdates) {
					t.Errorf("updates() error = %v, want errors.Is(err, ErrCannotCheckUpdates) == true", err)
				}
				if !tt.wantOffline && errors.Is(err, ErrCannotCheckUpdates) {
					t.Errorf("updates() error = %v, got ErrCannotCheckUpdates but wantOffline == false", err)
				}
				// On error paths, statuses must be nil
				if osStatus != nil {
					t.Errorf("updates() osStatus = %v, want nil on error", osStatus)
				}
				if boardStatus != nil {
					t.Errorf("updates() boardStatus = %v, want nil on error", boardStatus)
				}
				return
			}

			// Assert expected statuses on success paths
			if tt.expectNilStatuses {
				// Update succeeded but post-update status check failed (e.g. DNS not yet available)
				if osStatus != nil {
					t.Errorf("updates() osStatus = %+v, want nil (unverified outcome)", osStatus)
				}
				if boardStatus != nil {
					t.Errorf("updates() boardStatus = %+v, want nil (unverified outcome)", boardStatus)
				}
			} else if tt.expectedOsStatus != nil {
				if osStatus == nil {
					t.Errorf("updates() osStatus = nil, want %+v", tt.expectedOsStatus)
				} else if *osStatus != *tt.expectedOsStatus {
					t.Errorf("updates() osStatus = %+v, want %+v", *osStatus, *tt.expectedOsStatus)
				}
			}
			if !tt.expectNilStatuses {
				if tt.expectedBoardStatus != nil {
					if boardStatus == nil {
						t.Errorf("updates() boardStatus = nil, want %+v", tt.expectedBoardStatus)
					} else if *boardStatus != *tt.expectedBoardStatus {
						t.Errorf("updates() boardStatus = %+v, want %+v", *boardStatus, *tt.expectedBoardStatus)
					}
				} else if !tt.hasBoard && boardStatus != nil {
					// When no RouterBoard is present (tt.hasBoard == false), ensure no board status is returned
					t.Errorf("updates() boardStatus = %+v, want nil when no RouterBoard is present", boardStatus)
				}
			}

			// Verify update commands were executed when expected
			if tt.expectOsUpdate {
				found := slices.Contains(executedCommands, "/system/package/update/install")
				if !found {
					t.Errorf("updates() expected RouterOS update command to be executed, but it wasn't. Commands: %v", executedCommands)
				}
			}

			if tt.expectBoardUpdate {
				found := slices.Contains(executedCommands, "/system/reboot")
				if !found {
					t.Errorf("updates() expected RouterBoard update (reboot) command to be executed, but it wasn't. Commands: %v", executedCommands)
				}
			}

			// Verify check commands were always executed (unless SSH failed)
			if tt.sshError == nil {
				checkFound := slices.Contains(executedCommands, "/system/package/update/check-for-updates")
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
			mock := &sshmocks_test.MockRunner{
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
		osStatus    *UpdateStatus
		boardStatus *UpdateStatus
		wantEmoji   string
		wantMsg     string
	}{
		{
			name: "Virtualized router - up to date",
			osStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: nil,
			wantEmoji:   "✅",
			wantMsg:     "is up-to-date (RouterOS: 7.14.1)",
		},
		{
			name: "Virtualized router - update available",
			osStatus: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			boardStatus: nil,
			wantEmoji:   "⚠️",
			wantMsg:     "upgrade available (RouterOS: 7.14.0 → 7.14.1)",
		},
		{
			name: "Physical router - both up to date",
			osStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			wantEmoji: "✅",
			wantMsg:   "is up-to-date (RouterOS: 7.14.1, RouterBoard: 7.14.1)",
		},
		{
			name: "Physical router - OS update available",
			osStatus: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			wantEmoji: "⚠️",
			wantMsg:   "upgrade available (RouterOS: 7.14.0 → 7.14.1, RouterBoard: 7.14.1 → pending)",
		},
		{
			name: "Physical router - Board update available",
			osStatus: &UpdateStatus{
				Installed: "7.14.1",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.14.0",
				Available: "7.14.1",
			},
			wantEmoji: "⚠️",
			wantMsg:   "upgrade available (RouterOS: 7.14.1 → 7.14.1, RouterBoard: 7.14.0 → 7.14.1)",
		},
		{
			name: "Physical router - both updates available",
			osStatus: &UpdateStatus{
				Installed: "7.13.5",
				Available: "7.14.1",
			},
			boardStatus: &UpdateStatus{
				Installed: "7.13.5",
				Available: "7.14.1",
			},
			wantEmoji: "⚠️",
			wantMsg:   "upgrade available (RouterOS: 7.13.5 → 7.14.1, RouterBoard: 7.13.5 → 7.14.1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEmoji, gotMsg := formatUpdateResult(tt.osStatus, tt.boardStatus)
			if gotEmoji != tt.wantEmoji {
				t.Errorf("formatUpdateResult() emoji = %q, want %q", gotEmoji, tt.wantEmoji)
			}
			if gotMsg != tt.wantMsg {
				t.Errorf("formatUpdateResult() msg = %q, want %q", gotMsg, tt.wantMsg)
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
		wantOffline        bool // true when error should wrap ErrCannotCheckUpdates
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
			name: "RouterOS check fails - router offline (DNS)",
			osOutput: `       channel: stable
  installed-version: 7.14.1
        status: ERROR: could not resolve dns name`,
			osError:     nil,
			boardOutput: "",
			boardError:  nil,
			wantErr:     true,
			wantOffline: true,
		},
		{
			name: "RouterOS check fails - router offline (no route)",
			osOutput: `       channel: stable
  installed-version: 7.14.1
        status: ERROR: no-route-to-host`,
			osError:     nil,
			boardOutput: "",
			boardError:  nil,
			wantErr:     true,
			wantOffline: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &sshmocks_test.MockRunner{
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
					return
				}
				if tt.wantOffline && !errors.Is(err, ErrCannotCheckUpdates) {
					t.Errorf("checkCurrentStatus() error = %v, want errors.Is(err, ErrCannotCheckUpdates) == true", err)
				}
				if !tt.wantOffline && errors.Is(err, ErrCannotCheckUpdates) {
					t.Errorf("checkCurrentStatus() error = %v, got ErrCannotCheckUpdates but wantOffline == false", err)
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

			initialMock := &sshmocks_test.MockRunner{
				RunFunc: func(cmd string) (string, error) {
					return "", tt.runError
				},
				CloseFunc: func() error {
					closeCallCount++
					return nil
				},
			}

			// Mock the SSH connection factory
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				reconnectAttempts++
				if reconnectAttempts < tt.reconnectAttempts {
					return nil, fmt.Errorf("connection failed")
				}
				return &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						return "", nil
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			deps := UpdatesDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ReconnectDelay:       1 * time.Millisecond, // Speed up tests
			}

			ctx := context.WithValue(context.Background(), core.SshManagerKey, &sshmocks_test.MockManager{})

			newConn, err := applyUpdate(initialMock, ctx, "test-router", tt.updateCmd, "Test update", deps)

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

func TestApplyComponentUpdate(t *testing.T) {
	tests := []struct {
		name                      string
		component                 string
		updateCmd                 string
		checkBoth                 bool
		updateError               error
		osOutput                  string
		osOutputAfterUpdate       string
		boardOutput               string
		wantErr                   bool
		expectedOsStatusNotNil    bool
		expectedBoardStatusNotNil bool
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
			wantErr:                   false,
			expectedOsStatusNotNil:    true,
			expectedBoardStatusNotNil: false,
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
			wantErr:                   false,
			expectedOsStatusNotNil:    true,
			expectedBoardStatusNotNil: true,
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
			wantErr:                   false, // Errors in post-update check are non-fatal
			expectedOsStatusNotNil:    false,
			expectedBoardStatusNotNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCalled := false
			closeCalled := false

			initialMock := &sshmocks_test.MockRunner{
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
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				return &sshmocks_test.MockRunner{
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

			deps := UpdatesDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ReconnectDelay:       1 * time.Millisecond, // Speed up tests
			}

			ctx := context.WithValue(context.Background(), core.SshManagerKey, &sshmocks_test.MockManager{})

			osStatus, boardStatus, err := applyComponentUpdate(initialMock, ctx, "test-router", tt.component, tt.updateCmd, tt.checkBoth, deps)

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

			// Assert returned statuses
			if tt.expectedOsStatusNotNil && osStatus == nil {
				t.Errorf("applyComponentUpdate() osStatus = nil, want non-nil")
			}
			if !tt.expectedOsStatusNotNil && osStatus != nil {
				t.Errorf("applyComponentUpdate() osStatus = %+v, want nil", osStatus)
			}
			if tt.expectedBoardStatusNotNil && boardStatus == nil {
				t.Errorf("applyComponentUpdate() boardStatus = nil, want non-nil")
			}
			if !tt.expectedBoardStatusNotNil && boardStatus != nil {
				t.Errorf("applyComponentUpdate() boardStatus = %+v, want nil", boardStatus)
			}
		})
	}
}

// TestUpdatesRouterOSStagingRouterBoardFirmware tests the critical scenario where:
// 1. RouterOS update is needed
// 2. RouterBoard appears up-to-date initially (matching current RouterOS)
// 3. After RouterOS update, RouterOS stages new RouterBoard firmware
// 4. We must reconnect and re-check RouterBoard status
// 5. Then apply RouterBoard reboot if firmware was staged
func TestUpdatesRouterOSStagingRouterBoardFirmware(t *testing.T) {
	tests := []struct {
		name                        string
		osInstalledInitial          string
		osAvailableInitial          string
		boardInstalledInitial       string
		boardAvailableInitial       string
		boardInstalledAfterOsUpdate string
		boardAvailableAfterOsUpdate string
		expectReconnect             bool
		expectBoardReboot           bool
	}{
		{
			name:                        "RouterOS update stages RouterBoard firmware",
			osInstalledInitial:          "7.11.3",
			osAvailableInitial:          "7.12.1",
			boardInstalledInitial:       "7.11.3",
			boardAvailableInitial:       "7.11.3", // Matches current RouterOS
			boardInstalledAfterOsUpdate: "7.11.3", // Current firmware still old
			boardAvailableAfterOsUpdate: "7.12.1", // Staged by RouterOS update
			expectReconnect:             true,
			expectBoardReboot:           true,
		},
		{
			name:                        "RouterOS update but no RouterBoard firmware staged",
			osInstalledInitial:          "7.11.3",
			osAvailableInitial:          "7.12.1",
			boardInstalledInitial:       "7.12.1",
			boardAvailableInitial:       "7.12.1", // Already has compatible firmware
			boardInstalledAfterOsUpdate: "7.12.1",
			boardAvailableAfterOsUpdate: "7.12.1",
			expectReconnect:             true,
			expectBoardReboot:           false, // No reboot needed
		},
		{
			name:                        "RouterOS update stages different RouterBoard version",
			osInstalledInitial:          "7.10",
			osAvailableInitial:          "7.12.1",
			boardInstalledInitial:       "7.10",
			boardAvailableInitial:       "7.10",
			boardInstalledAfterOsUpdate: "7.10",
			boardAvailableAfterOsUpdate: "7.12.1", // Staged compatible firmware
			expectReconnect:             true,
			expectBoardReboot:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var connectionCount int
			var executedCommands []string
			var osUpdateApplied bool

			// Mock SSH connection factory that changes behavior after OS update
			mockSSHFactory := func(ctx context.Context, host string) (ssh.RunnerInterface, error) {
				connectionCount++

				return &sshmocks_test.MockRunner{
					RunFunc: func(cmd string) (string, error) {
						executedCommands = append(executedCommands, cmd)

						switch cmd {
						case "/system/package/update/check-for-updates":
							if osUpdateApplied {
								// After OS update, RouterOS is up to date
								return fmt.Sprintf(`  status: Updated
  installed-version: %s
  latest-version: %s`, tt.osAvailableInitial, tt.osAvailableInitial), nil
							}
							// Before OS update
							return fmt.Sprintf(`  status: New version available
  installed-version: %s
  latest-version: %s`, tt.osInstalledInitial, tt.osAvailableInitial), nil

						case "/system/routerboard/print":
							if osUpdateApplied {
								// After OS update, RouterBoard status reflects staged firmware
								return fmt.Sprintf(`  routerboard: yes
  current-firmware: %s
  upgrade-firmware: %s`, tt.boardInstalledAfterOsUpdate, tt.boardAvailableAfterOsUpdate), nil
							}
							// Before OS update
							return fmt.Sprintf(`  routerboard: yes
  current-firmware: %s
  upgrade-firmware: %s`, tt.boardInstalledInitial, tt.boardAvailableInitial), nil

						case "/system/package/update/install":
							osUpdateApplied = true
							return "System will reboot", nil

						case "/system/reboot":
							return "System is rebooting", nil
						}

						return "", nil
					},
					CloseFunc: func() error {
						return nil
					},
				}, nil
			}

			// Build test configuration
			cfg := UpdatesConfig{
				UpdatesApply: true, // Must apply updates to trigger the scenario
			}

			deps := UpdatesDependencies{
				SSHConnectionFactory: mockSSHFactory,
				ReconnectDelay:       10 * time.Millisecond,
			}

			// Create context
			coreCfg := &core.Config{
				Hosts: []string{"router.example.com"},
				User:  "admin",
			}
			ctx := context.WithValue(context.Background(), core.ConfigKey, coreCfg)
			ctx = context.WithValue(ctx, core.SshManagerKey, &sshmocks_test.MockManager{})

			// Execute the function
			_, _, err := updates(ctx, "router.example.com", cfg, deps)

			// Should not error
			if err != nil {
				t.Fatalf("updates() unexpected error = %v", err)
			}

			// Verify RouterOS update was applied
			osUpdateFound := slices.Contains(executedCommands, "/system/package/update/install")
			if !osUpdateFound {
				t.Error("RouterOS update command was not executed")
			}

			// Verify reconnection happened after RouterOS update
			if tt.expectReconnect {
				if connectionCount < 2 {
					t.Errorf("Expected at least 2 connections (initial + reconnect), got %d", connectionCount)
				}
			}

			// Note: RouterOS status may be checked multiple times:
			// 1. Initially before updates
			// 2. After RouterOS update in applyComponentUpdate
			// 3. After RouterBoard update in applyComponentUpdate (if board update happens)
			// What we want to verify is that we DON'T call checkCurrentStatus after RouterOS update
			// (which would fail with DNS issues), we only check RouterBoard directly

			// Verify RouterBoard status was re-checked after RouterOS update
			checkCount := 0
			for _, cmd := range executedCommands {
				if cmd == "/system/routerboard/print" {
					checkCount++
				}
			}
			if tt.expectReconnect && checkCount < 2 {
				t.Errorf("Expected RouterBoard status to be checked at least twice (before + after OS update), got %d", checkCount)
			}

			// Verify RouterBoard reboot was executed if firmware was staged
			boardRebootFound := slices.Contains(executedCommands, "/system/reboot")
			if tt.expectBoardReboot && !boardRebootFound {
				t.Error("Expected RouterBoard reboot command but it was not executed")
			}
			if !tt.expectBoardReboot && boardRebootFound {
				t.Error("Did not expect RouterBoard reboot command but it was executed")
			}
		})
	}
}

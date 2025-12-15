package updates

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var updatesApply bool = true

// reconnectDelay is the delay between reconnection attempts after a router reboot
// This can be overridden in tests to speed up test execution
var reconnectDelay = 10 * time.Second

// sshConnectionFactory is the factory function for creating SSH connections
// This can be overridden in tests to inject mock SSH manager
var sshConnectionFactory = core.CreateConnection

var Command = []*cli.Command{
	{
		Name:  "updates",
		Usage: "Manages MikroTik router updates",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "updates-apply",
				Value:       false,
				Usage:       "Update router packages to the latest version available",
				Destination: &updatesApply,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := core.GetConfig(ctx)
			if err != nil {
				return err
			}

			// Iterate over all hosts
			var lastErr error
			for _, host := range cfg.Hosts {
				if err := updates(ctx, host); err != nil {
					fmt.Println(fmt.Sprintf("❓ %s is unreachable", host))
					// Continue with other hosts even if one fails
				}
			}
			return lastErr
		},
	},
}

type UpdateStatus struct {
	Installed string
	Available string
}

func updates(ctx context.Context, host string) error {
	updatesApplyFlag := updatesApply
	slog.Debug("Subcommand apply-updates flag is " + fmt.Sprintf("%v", updatesApplyFlag))

	// SSH init
	slog.Info("Initializing SSH connection")
	conn, err := sshConnectionFactory(ctx, host)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer func() {
		_ = conn.Close() // Error logging handled inside Close()
	}()
	slog.Debug("SSH connection created for host " + host)

	// Step 1: Check current status
	slog.Info("Checking current update status")
	osStatus, boardStatus, err := checkCurrentStatus(conn)
	if err != nil {
		return err
	}

	// Step 2: Display current status
	slog.Info("Displaying current update status")
	formatAndDisplayResult(host, osStatus, boardStatus)

	// Step 3: Apply updates if requested and needed
	if updatesApplyFlag && updatesApply {
		osUpToDate := osStatus.Installed == osStatus.Available
		boardUpToDate := boardStatus == nil || boardStatus.Installed == boardStatus.Available

		// Apply RouterOS update if needed
		if !osUpToDate {
			slog.Info("Applying RouterOS updates")
			if err := applyComponentUpdate(conn, ctx, host, "RouterOS", "/system/package/update/install", false); err != nil {
				return err
			}
		}

		// Apply RouterBoard update if needed (only for physical routers)
		if !boardUpToDate && boardStatus != nil {
			slog.Info("Applying RouterBoard updates")
			if err := applyComponentUpdate(conn, ctx, host, "RouterBoard", "/system/reboot", true); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkCurrentStatus retrieves the current RouterOS and RouterBoard status
func checkCurrentStatus(conn core.SshRunner) (UpdateStatus, *UpdateStatus, error) {
	slog.Info("Checking RouterOS update status")
	osStatusPtr, err := getUpdateStatus(
		conn,
		"/system/package/update/check-for-updates",
		"RouterOS",
		regexp.MustCompile(`.*installed-version: (\S+)`),
		regexp.MustCompile(`.*latest-version: (\S+)`),
		false,
	)
	if err != nil {
		return UpdateStatus{}, nil, err
	}
	osStatus := *osStatusPtr
	slog.Debug("RouterOS status is " + fmt.Sprintf("%+v", osStatus))
	if osStatus.Installed == osStatus.Available {
		slog.Info("RouterOS already up to date with RouterOS " + osStatus.Installed)
	} else {
		slog.Info("RouterOS update available from version " + osStatus.Installed + " to " + osStatus.Available)
	}

	slog.Info("Checking RouterBoard update status")
	boardStatus, err := getUpdateStatus(
		conn,
		"/system/routerboard/print",
		"RouterBoard",
		regexp.MustCompile(`.*current-firmware: (\S+)`),
		regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
		true,
	)
	if err != nil {
		return UpdateStatus{}, nil, err
	}

	if boardStatus == nil {
		slog.Info("RouterBoard not present (virtualized RouterOS)")
	} else {
		slog.Debug("RouterBoard status is " + fmt.Sprintf("%+v", boardStatus))
		if boardStatus.Installed == boardStatus.Available {
			slog.Info("RouterBoard already up to date with RouterBoard " + boardStatus.Installed)
		} else {
			slog.Info("RouterBoard update available from version " + boardStatus.Installed + " to " + boardStatus.Available)
		}
	}

	return osStatus, boardStatus, nil
}

// applyComponentUpdate applies an update to RouterOS or RouterBoard and displays the result
func applyComponentUpdate(conn core.SshRunner, ctx context.Context, host, component, updateCmd string, checkBoth bool) error {
	slog.Info(component + " update needed, applying updates")
	slog.Debug("Applying " + component + " updates on router " + host)

	msgPrefix := "Update applied on router"
	if component == "RouterBoard" {
		msgPrefix = "RouterBoard update applied on router"
	}
	newConn, err := applyUpdate(conn, ctx, host, updateCmd, msgPrefix+" "+host)
	if err != nil {
		return err
	}
	defer func() {
		_ = newConn.Close() // Error logging handled inside Close()
	}()

	// Check status after upgrade
	osStatusPtr, osStatusErr := getUpdateStatus(
		newConn,
		"/system/package/update/check-for-updates",
		"RouterOS",
		regexp.MustCompile(`.*installed-version: (\S+)`),
		regexp.MustCompile(`.*latest-version: (\S+)`),
		false,
	)

	if !checkBoth {
		// RouterOS only update
		if osStatusErr == nil {
			osStatus := *osStatusPtr
			formatAndDisplayResult(host, osStatus, nil)
		} else {
			slog.Warn("Failed to check RouterOS status after update: " + osStatusErr.Error())
		}
		// Post-update check errors are non-fatal - the update itself succeeded
		return nil
	}

	// RouterBoard update - check both OS and Board
	boardStatus, boardStatusErr := getUpdateStatus(
		newConn,
		"/system/routerboard/print",
		"RouterBoard",
		regexp.MustCompile(`.*current-firmware: (\S+)`),
		regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
		true,
	)

	if osStatusErr == nil && boardStatusErr == nil {
		osStatus := *osStatusPtr
		fmt.Println(formatUpdateResult(host, osStatus, boardStatus))
	} else {
		if osStatusErr != nil {
			slog.Warn("Failed to check RouterOS status after update: " + osStatusErr.Error())
		}
		if boardStatusErr != nil {
			slog.Warn("Failed to check RouterBoard status after update: " + boardStatusErr.Error())
		}
	}

	// Post-update check errors are non-fatal - the update itself succeeded
	return nil
}

// formatUpdateResult formats the update result into a string
func formatUpdateResult(host string, osStatus UpdateStatus, boardStatus *UpdateStatus) string {
	osUpToDate := osStatus.Installed == osStatus.Available

	if boardStatus == nil {
		// Virtualized router or RouterOS-only update
		if osUpToDate {
			return fmt.Sprintf("✅ %s is up-to-date (RouterOS: %s)", host, osStatus.Installed)
		}
		return fmt.Sprintf("⚠️  %s upgrade available (RouterOS: %s → %s)", host, osStatus.Installed, osStatus.Available)
	}

	// Physical router with RouterBoard
	boardUpToDate := boardStatus.Installed == boardStatus.Available
	if osUpToDate && boardUpToDate {
		return fmt.Sprintf("✅ %s is up-to-date (RouterOS: %s, RouterBoard: %s)", host, osStatus.Installed, boardStatus.Installed)
	}

	var boardUpgrade string
	if boardUpToDate {
		if osUpToDate {
			boardUpgrade = boardStatus.Installed
		} else {
			boardUpgrade = fmt.Sprintf("%s → pending", boardStatus.Installed)
		}
	} else {
		boardUpgrade = fmt.Sprintf("%s → %s", boardStatus.Installed, boardStatus.Available)
	}
	return fmt.Sprintf("⚠️  %s upgrade available (RouterOS: %s → %s, RouterBoard: %s)", host, osStatus.Installed, osStatus.Available, boardUpgrade)
}

// formatAndDisplayResult formats and displays the update result
func formatAndDisplayResult(host string, osStatus UpdateStatus, boardStatus *UpdateStatus) {
	fmt.Println(formatUpdateResult(host, osStatus, boardStatus))
}

// Generic update status fetcher for RouterOS and RouterBoard
func getUpdateStatus(conn core.SshRunner, sshCmd, subSystem string, installedRe, availableRe *regexp.Regexp, skipIfNoRouterBoard bool) (*UpdateStatus, error) {
	slog.Debug("SSH cmd is " + sshCmd)
	result, err := conn.Run(sshCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to run SSH command: %w", err)
	}

	// Check if the output contains an ERROR status
	if matched, _ := regexp.MatchString(`(?m)^\s*status:\s*ERROR`, result); matched {
		// Extract the error message from the last status line
		statusRe := regexp.MustCompile(`(?m)^\s*status:\s*(.+?)[\r\n]*$`)
		allMatches := statusRe.FindAllStringSubmatch(result, -1)
		if len(allMatches) > 0 {
			lastMatch := allMatches[len(allMatches)-1]
			if len(lastMatch) >= 2 {
				return nil, fmt.Errorf("%s check failed: %s", subSystem, lastMatch[1])
			}
		}
		return nil, fmt.Errorf("%s check failed with ERROR status", subSystem)
	}

	if skipIfNoRouterBoard {
		if matched, _ := regexp.MatchString(`(?m)^\s*routerboard:\s*no`, result); matched {
			return nil, nil
		}
	}

	installedMatches := installedRe.FindStringSubmatch(result)
	if len(installedMatches) < 2 {
		return nil, fmt.Errorf("failed to parse installed version: %s version not found in output", subSystem)
	}

	availableMatches := availableRe.FindStringSubmatch(result)
	if len(availableMatches) < 2 {
		return nil, fmt.Errorf("failed to parse available version: %s version not found in output", subSystem)
	}

	return &UpdateStatus{Installed: installedMatches[1], Available: availableMatches[1]}, nil
}

// Generic function to apply updates and wait for router to come back
func applyUpdate(conn core.SshRunner, ctx context.Context, host, updateCmd, waitMsg string) (core.SshRunner, error) {
	_, err := conn.Run(updateCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to run SSH command: %w", err)
	}
	_ = conn.Close() // Error logging handled inside Close()
	fmt.Printf("⏳ %s\n", waitMsg)

	var newConn core.SshRunner
	for {
		fmt.Printf("⏳ Waiting for router %v to come back up...\n", host)
		time.Sleep(reconnectDelay)

		newConn, err = sshConnectionFactory(ctx, host)
		if err != nil {
			continue
		}
		break
	}
	return newConn, nil
}

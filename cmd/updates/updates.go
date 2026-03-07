package updates

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// UpdatesConfig holds all updates configuration options
type UpdatesConfig struct {
	UpdatesApply bool
}

// UpdatesDependencies holds injectable dependencies for testing
type UpdatesDependencies struct {
	SSHConnectionFactory func(context.Context, string) (ssh.RunnerInterface, error)
	ReconnectDelay       time.Duration
}

var Command = []*cli.Command{
	{
		Name:  "updates",
		Usage: "Manages MikroTik router updates",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "updates-apply",
				Value: false,
				Usage: "Update router packages to the latest version available",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			coreCfg, err := core.GetConfig(ctx)
			if err != nil {
				return err
			}

			// Build updates configuration from CLI flags
			updatesCfg := UpdatesConfig{
				UpdatesApply: cmd.Bool("updates-apply"),
			}

			// Build dependencies
			deps := UpdatesDependencies{
				SSHConnectionFactory: ssh.CreateConnection,
				ReconnectDelay:       10 * time.Second,
			}

			// Iterate over all hosts
			var lastErr error
			for _, host := range coreCfg.Hosts {
				osStatus, boardStatus, err := updates(ctx, host, updatesCfg, deps)
				if err != nil {
					fmt.Printf("❌ %s: Updates failed\n", host)
					lastErr = err
					// Continue with other hosts even if one fails
				} else if osStatus == nil {
					fmt.Printf("❓ %s: Update applied, status could not be verified\n", host)
				} else {
					fmt.Println(formatUpdateResult(host, osStatus, boardStatus))
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

// Updates is a public wrapper that applies updates to a single host
// This function is intended to be called from other subcommands like enroll
func Updates(ctx context.Context, host string) error {
	cfg := UpdatesConfig{
		UpdatesApply: true,
	}
	deps := UpdatesDependencies{
		SSHConnectionFactory: ssh.CreateConnection,
		ReconnectDelay:       10 * time.Second,
	}
	_, _, err := updates(ctx, host, cfg, deps) // statuses intentionally discarded; caller (enroll) manages output
	return err
}

func updates(ctx context.Context, host string, cfg UpdatesConfig, deps UpdatesDependencies) (*UpdateStatus, *UpdateStatus, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context cancelled: %w", err)
	}

	slog.Debug("subcommand apply-updates flag", "value", cfg.UpdatesApply)

	// SSH init
	slog.Info("Initializing SSH connection")
	conn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		slog.Debug("failed to create SSH connection", "host", host, "error", err)
		return nil, nil, fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	slog.Debug("SSH connection created", "host", host)

	// Step 1: Check current status
	slog.Info("Checking current update status")
	osStatus, boardStatus, err := checkCurrentStatus(conn)
	if err != nil {
		return nil, nil, err
	}

	// Step 2: Apply updates if requested and needed
	if !cfg.UpdatesApply {
		// Only checking updates, not applying
		slog.Info("Updates apply not requested, skipping update application")
		return osStatus, boardStatus, nil
	}

	// Track the final statuses to return to the caller
	finalOsStatus := osStatus
	finalBoardStatus := boardStatus

	osUpToDate := osStatus.Installed == osStatus.Available
	boardUpToDate := boardStatus == nil || boardStatus.Installed == boardStatus.Available

	// Apply RouterOS update if needed
	if !osUpToDate {
		slog.Info("Applying RouterOS updates")
		newOsStatus, newBoardStatus, err := applyComponentUpdate(conn, ctx, host, "RouterOS", "/system/package/update/install", false, deps)
		if err != nil {
			return nil, nil, err
		}
		// nil means the update succeeded but the post-update status check failed (e.g. DNS not yet available)
		finalOsStatus = newOsStatus
		if newBoardStatus != nil {
			finalBoardStatus = newBoardStatus
		}
		// After RouterOS update, reconnect and re-check RouterBoard status only
		// (RouterOS update may have staged new RouterBoard firmware)
		// We don't need to re-check RouterOS status since we just updated it,
		// and checking RouterOS requires DNS which may not be available yet,
		// specifically for core routers due to internet not being connected yet.
		slog.Debug("reconnecting after RouterOS update to re-check RouterBoard status", "host", host)
		// Force connection close before reconnecting to avoid resource leaks
		_ = conn.Close()

		if boardStatus != nil {
			conn, err = deps.SSHConnectionFactory(ctx, host)
			if err != nil {
				slog.Error("failed to reconnect after RouterOS update", "host", host, "error", err)
				return nil, nil, fmt.Errorf("failed to reconnect after RouterOS update: %w", err)
			}
			defer func() {
				_ = conn.Close()
			}()

			// Only re-check RouterBoard status (not RouterOS which requires working DNS)
			slog.Info("Re-checking RouterBoard status after RouterOS update")
			recheckBoardStatus, recheckErr := getUpdateStatus(
				conn,
				"/system/routerboard/print",
				"RouterBoard",
				regexp.MustCompile(`.*current-firmware: (\S+)`),
				regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
				true,
			)
			if recheckErr != nil {
				slog.Warn("failed to re-check RouterBoard status after RouterOS update", "error", recheckErr)
				// Keep the pre-update boardStatus so the RouterBoard-update condition still runs
			} else if recheckBoardStatus != nil {
				boardStatus = recheckBoardStatus
				finalBoardStatus = recheckBoardStatus
				boardUpToDate = boardStatus.Installed == boardStatus.Available
				slog.Info("RouterBoard status after RouterOS update",
					"current", boardStatus.Installed,
					"upgrade", boardStatus.Available,
					"upToDate", boardUpToDate)
			}
		}
	}

	// Apply RouterBoard update if needed (only for physical routers)
	if !boardUpToDate && boardStatus != nil {
		slog.Info("Applying RouterBoard updates")
		newOsStatus, newBoardStatus, err := applyComponentUpdate(conn, ctx, host, "RouterBoard", "/system/reboot", true, deps)
		if err != nil {
			return nil, nil, err
		}
		// nil means the update succeeded but the post-update status check failed (e.g. DNS not yet available)
		finalOsStatus = newOsStatus
		finalBoardStatus = newBoardStatus
	}

	if finalOsStatus == nil {
		// At least one update was applied but the post-update status check failed (e.g. DNS not yet available)
		slog.Warn("post-update status check failed, update outcome unverified", "host", host)
		return nil, nil, nil
	}

	return finalOsStatus, finalBoardStatus, nil
}

// checkCurrentStatus retrieves the current RouterOS and RouterBoard status
func checkCurrentStatus(conn ssh.RunnerInterface) (*UpdateStatus, *UpdateStatus, error) {
	slog.Info("Checking RouterOS update status")
	osStatus, err := getUpdateStatus(
		conn,
		"/system/package/update/check-for-updates",
		"RouterOS",
		regexp.MustCompile(`.*installed-version: (\S+)`),
		regexp.MustCompile(`.*latest-version: (\S+)`),
		false,
	)
	if err != nil {
		return nil, nil, err
	}
	slog.Debug("RouterOS status", "value", osStatus)
	if osStatus.Installed == osStatus.Available {
		slog.Info("RouterOS already up to date", "version", osStatus.Installed)
	} else {
		slog.Info("RouterOS update available", "from", osStatus.Installed, "to", osStatus.Available)
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
		return nil, nil, err
	}

	if boardStatus == nil {
		slog.Info("RouterBoard not present (virtualized RouterOS)")
	} else {
		slog.Debug("RouterBoard status", "value", boardStatus)
		if boardStatus.Installed == boardStatus.Available {
			slog.Info("RouterBoard already up to date", "version", boardStatus.Installed)
		} else {
			slog.Info("RouterBoard update available", "from", boardStatus.Installed, "to", boardStatus.Available)
		}
	}

	return osStatus, boardStatus, nil
}

// applyComponentUpdate applies an update to RouterOS or RouterBoard and returns the resulting statuses
func applyComponentUpdate(conn ssh.RunnerInterface, ctx context.Context, host, component, updateCmd string, checkBoth bool, deps UpdatesDependencies) (*UpdateStatus, *UpdateStatus, error) {
	slog.Info("component update needed, applying updates", "component", component)
	slog.Debug("applying component updates", "component", component, "host", host)

	msgPrefix := "Update applied on router"
	if component == "RouterBoard" {
		msgPrefix = "RouterBoard update applied on router"
	}
	newConn, err := applyUpdate(conn, ctx, host, updateCmd, msgPrefix+" "+host, deps)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = newConn.Close()
	}()

	// Check status after upgrade
	osStatus, osStatusErr := getUpdateStatus(
		newConn,
		"/system/package/update/check-for-updates",
		"RouterOS",
		regexp.MustCompile(`.*installed-version: (\S+)`),
		regexp.MustCompile(`.*latest-version: (\S+)`),
		false,
	)

	if !checkBoth {
		// RouterOS only update
		if osStatusErr != nil {
			slog.Warn("failed to check RouterOS status after update", "error", osStatusErr)
			// Post-update check errors are non-fatal - the update itself succeeded
			return nil, nil, nil
		}
		// Post-update check errors are non-fatal - the update itself succeeded
		return osStatus, nil, nil
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
		return osStatus, boardStatus, nil
	}
	if osStatusErr != nil {
		slog.Warn("failed to check RouterOS status after update", "error", osStatusErr)
	}
	if boardStatusErr != nil {
		slog.Warn("failed to check RouterBoard status after update", "error", boardStatusErr)
	}

	// Post-update check errors are non-fatal - the update itself succeeded
	return nil, nil, nil
}

// formatUpdateResult formats the update result into a string
func formatUpdateResult(host string, osStatus *UpdateStatus, boardStatus *UpdateStatus) string {
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

// Generic update status fetcher for RouterOS and RouterBoard
func getUpdateStatus(conn ssh.RunnerInterface, sshCmd string, subSystem string, installedRe *regexp.Regexp, availableRe *regexp.Regexp, skipIfNoRouterBoard bool) (*UpdateStatus, error) {
	slog.Debug("executing command", "command", sshCmd)
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
func applyUpdate(conn ssh.RunnerInterface, ctx context.Context, host string, updateCmd string, waitMsg string, deps UpdatesDependencies) (ssh.RunnerInterface, error) {
	_, err := conn.Run(updateCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to run SSH command: %w", err)
	}
	_ = conn.Close()
	fmt.Printf("⏳ %s\n", waitMsg)

	var newConn ssh.RunnerInterface
	for {
		// Check if context was cancelled during reconnection
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during reconnection: %w", err)
		}

		fmt.Printf("⏳ Waiting for router %v to come back up...\n", host)
		time.Sleep(deps.ReconnectDelay)

		newConn, err = deps.SSHConnectionFactory(ctx, host)
		if err != nil {
			continue
		}
		break
	}
	return newConn, nil
}

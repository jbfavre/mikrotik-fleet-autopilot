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

var applyUpdates bool = true

// reconnectDelay is the delay between reconnection attempts after a router reboot
// This can be overridden in tests to speed up test execution
var reconnectDelay = 10 * time.Second

// sshConnectionFactory is the factory function for creating SSH connections
// This can be overridden in tests to inject mock connections
var sshConnectionFactory = func(host, user, password string) (core.SshRunner, error) {
	return core.NewSsh(host, user, password)
}

var Command = []*cli.Command{
	{
		Name:  "updates",
		Usage: "Manages MikroTik router updates",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "apply-updates",
				Value:       false,
				Usage:       "Update router packages to the latest version available",
				Destination: &applyUpdates,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := core.GetConfig(ctx)
			if err != nil {
				return err
			}
			return updates(cfg)
		},
	},
}

type UpdateStatus struct {
	Installed string
	Available string
}

func init() {}

func updates(cfg *core.Config) error {
	applyUpdatesFlag := applyUpdates
	slog.Debug("Apply updates flag from cmd is " + fmt.Sprintf("%v", applyUpdatesFlag))

	// SSH init
	conn, err := sshConnectionFactory(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.Debug("failed to close SSH connection: " + closeErr.Error())
		}
	}()

	// Step 1: Check current status
	osStatus, boardStatus, err := checkCurrentStatus(conn)
	if err != nil {
		return err
	}

	// Step 2: Display current status
	formatAndDisplayResult(cfg.Host, osStatus, boardStatus)

	// Step 3: Apply updates if requested and needed
	if applyUpdatesFlag && applyUpdates {
		return applyUpdatesIfNeeded(conn, cfg, osStatus, boardStatus)
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
		if boardStatus.Installed == boardStatus.Available {
			slog.Info("RouterBoard already up to date with RouterBoard " + boardStatus.Installed)
		} else {
			slog.Info("RouterBoard update available from version " + boardStatus.Installed + " to " + boardStatus.Available)
		}
	}

	return osStatus, boardStatus, nil
}

// applyUpdatesIfNeeded applies RouterOS and RouterBoard updates if they are needed
func applyUpdatesIfNeeded(conn core.SshRunner, cfg *core.Config, osStatus UpdateStatus, boardStatus *UpdateStatus) error {
	osUpToDate := osStatus.Installed == osStatus.Available
	boardUpToDate := boardStatus == nil || boardStatus.Installed == boardStatus.Available

	// Apply RouterOS update if needed
	if !osUpToDate {
		if err := applyComponentUpdate(conn, cfg, "RouterOS", "/system/package/update/install", false); err != nil {
			return err
		}
	}

	// Apply RouterBoard update if needed (only for physical routers)
	if !boardUpToDate && boardStatus != nil {
		if err := applyComponentUpdate(conn, cfg, "RouterBoard", "/system/reboot", true); err != nil {
			return err
		}
	}

	return nil
}

// applyComponentUpdate applies an update to RouterOS or RouterBoard and displays the result
func applyComponentUpdate(conn core.SshRunner, cfg *core.Config, component, updateCmd string, checkBoth bool) error {
	slog.Info(component + " update needed, applying updates")
	slog.Debug("Applying " + component + " updates on router " + cfg.Host)

	msgPrefix := "Update applied on router"
	if component == "RouterBoard" {
		msgPrefix = "RouterBoard update applied on router"
	}
	newConn, err := applyUpdate(conn, cfg, updateCmd, msgPrefix+" "+cfg.Host)
	if err != nil {
		return err
	}
	defer func() {
		// Silently ignore close errors as connection may already be closed
		_ = newConn.Close()
	}()

	// Check status after upgrade
	osStatusPtr, err := getUpdateStatus(
		newConn,
		"/system/package/update/check-for-updates",
		"RouterOS",
		regexp.MustCompile(`.*installed-version: (\S+)`),
		regexp.MustCompile(`.*latest-version: (\S+)`),
		false,
	)

	if !checkBoth {
		// RouterOS only update
		if err == nil {
			osStatus := *osStatusPtr
			formatAndDisplayResult(cfg.Host, osStatus, nil)
		} else {
			slog.Warn("Failed to check RouterOS status after update: " + err.Error())
		}
		// Post-update check errors are non-fatal - the update itself succeeded
		return nil
	}

	// RouterBoard update - check both OS and Board
	boardStatus, err2 := getUpdateStatus(
		newConn,
		"/system/routerboard/print",
		"RouterBoard",
		regexp.MustCompile(`.*current-firmware: (\S+)`),
		regexp.MustCompile(`.*upgrade-firmware: (\S+)`),
		true,
	)

	if err == nil && err2 == nil {
		osStatus := *osStatusPtr
		fmt.Println(formatUpdateResult(cfg.Host, osStatus, boardStatus))
	} else {
		if err != nil {
			slog.Warn("Failed to check RouterOS status after update: " + err.Error())
		}
		if err2 != nil {
			slog.Warn("Failed to check RouterBoard status after update: " + err2.Error())
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
func applyUpdate(conn core.SshRunner, cfg *core.Config, updateCmd, waitMsg string) (core.SshRunner, error) {
	_, err := conn.Run(updateCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to run SSH command: %w", err)
	}
	if closeErr := conn.Close(); closeErr != nil {
		slog.Debug("failed to close SSH connection: " + closeErr.Error())
	}
	fmt.Printf("⏳ %s\n", waitMsg)

	var newConn core.SshRunner
	for {
		fmt.Printf("⏳ Waiting for router %v to come back up...\n", cfg.Host)
		time.Sleep(reconnectDelay)

		newConn, err = sshConnectionFactory(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
		if err != nil {
			continue
		}
		break
	}
	return newConn, nil
}

package updates

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"time"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/core"
)

var applyUpdates bool = true

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
			return updates(ctx, cmd, cfg)
		},
	},
}

func init() {}

func updates(ctx context.Context, cmd *cli.Command, cfg *core.Config) error {
	applyUpdatesFlag := applyUpdates
	slog.Debug("Apply updates flag from cmd is " + fmt.Sprintf("%v", applyUpdatesFlag))
	// SSH init
	conn, err := sshInit(cfg)
	if err != nil {
		return fmt.Errorf("failed to create SSH connection: %w", err)
	}
	defer conn.Close()

	// Manage RouterOS updates if any
	err = routerOsManageUpdates(conn, cfg, applyUpdatesFlag)
	if err != nil {
		return err
	}

	// Manage Routerboard updates if any
	err = routerBoardManageUpdates(conn, cfg, applyUpdatesFlag)
	if err != nil {
		return err
	}

	// Run SSH command to check for updates
	// If an update is available AND apply is selected,
	// 		Run SSH command to check partitions
	// 		If partition is available
	// 			Backup current running configuration on the backup partition
	// 		Run SSH command to apply updates
	// 		Ping router to check it's back up
	// 		Run SSH command to verify Routerboard status
	// 		Run SSH command to reboot if needed
	// 		Ping router to check it's back up
	return nil
}

func routerBoardManageUpdates(conn *core.SshConnection, cfg *core.Config, applyUpdatesFlag bool) error {
	updateNeeded, err := routerBoardCheckForUpdate(*conn, cfg)
	if err != nil {
		return fmt.Errorf("Failed to check for Routerboard updates: %w", err)
	}
	// If we don't have any update available, there's no need to trigger the update part
	// even if the flag has been provided
	applyUpdates = updateNeeded && applyUpdatesFlag

	// Apply it if needed
	if applyUpdates {
		slog.Debug("Updating Routerboard on router " + cfg.Host)
		sshCmd := "/system/reboot"
		_, err := conn.Run(sshCmd)
		if err != nil {
			return fmt.Errorf("failed to run SSH command: %w", err)
		}
		conn.Close()
		fmt.Printf("⏳ Update applied on router %v\n", cfg.Host)

		conn = nil
		for conn == nil {
			fmt.Printf("⏳ Waiting for router %v to come back up...\n", cfg.Host)
			time.Sleep(10 * time.Second)

			conn, err := sshInit(cfg)
			if err != nil {
				conn = nil
				continue
			}
			defer conn.Close()

			updateNeeded, err := routerBoardCheckForUpdate(*conn, cfg)
			if err != nil {
				continue
			} else {
				applyUpdates = updateNeeded && applyUpdatesFlag
				break
			}
		}
	}
	return nil
}

func routerOsManageUpdates(conn *core.SshConnection, cfg *core.Config, applyUpdatesFlag bool) error {
	updateNeeded, err := routerOsCheckForUpdate(*conn, cfg)
	if err != nil {
		return fmt.Errorf("Failed to check for RouterOS updates: %w", err)
	}
	// If we don't have any update available, there's no need to trigger the update part
	// even if the flag has been provided
	applyUpdates = updateNeeded && applyUpdatesFlag

	// Apply it if needed
	if applyUpdates {
		slog.Debug("Applying RouterOS updates on router " + cfg.Host)
		sshCmd := "/system/package/update/install"
		_, err := conn.Run(sshCmd)
		if err != nil {
			return fmt.Errorf("failed to run SSH command: %w", err)
		}
		conn.Close()
		fmt.Printf("⏳ Update applied on router %v\n", cfg.Host)

		conn = nil
		for conn == nil {
			fmt.Printf("⏳ Waiting for router %v to come back up...\n", cfg.Host)
			time.Sleep(10 * time.Second)

			conn, err := sshInit(cfg)
			if err != nil {
				conn = nil
				continue
			}
			defer conn.Close()

			updateNeeded, err := routerOsCheckForUpdate(*conn, cfg)
			if err != nil {
				continue
			} else {
				applyUpdates = updateNeeded && applyUpdatesFlag
				break
			}
		}
	}
	return nil
}

func routerOsCheckForUpdate(conn core.SshConnection, cfg *core.Config) (bool, error) {
	sshCmd := "/system/package/update/check-for-updates"
	updateNeeded := false

	slog.Debug("SSH cmd is " + sshCmd)
	result, err := conn.Run(sshCmd)
	if err != nil {
		return updateNeeded, fmt.Errorf("failed to run SSH command: %w", err)
	}

	//result = "            channel: stable \r\n  installed-version: 7.20.2 \r\n     latest-version: 7.20.5 \r\n             status: New version is available\r\n \r\n"
	slog.Debug("SSH command result:" + result)

	// Check for installed version
	installedVersion, installedError := checkVersion(regexp.MustCompile(`.*installed-version: (\S+)`), result)
	if installedError != nil {
		return updateNeeded, fmt.Errorf("failed to parse installed version: %v", installedError)
	}
	// Check for available version
	availableVersion, availableError := checkVersion(regexp.MustCompile(`.*latest-version: (\S+)`), result)
	if availableError != nil {
		return updateNeeded, fmt.Errorf("failed to parse available version: %v", availableError)
	}

	// Asses wether an update is needed or not
	if installedVersion == availableVersion {
		slog.Info("RouterOS already up to date with RouterOS " + installedVersion)
		fmt.Printf("✅ Router %v is up-to-date running RouterOS %v\n", cfg.Host, installedVersion)
	} else {
		slog.Info("RouterOS update available from version " + installedVersion + " to " + availableVersion)
		fmt.Printf("⚠️  Router %v can be upgraded from RouterOS %v to %v\n", cfg.Host, installedVersion, availableVersion)
		updateNeeded = true
	}
	return updateNeeded, nil
}

func routerBoardCheckForUpdate(conn core.SshConnection, cfg *core.Config) (bool, error) {
	updateNeeded := false

	sshCmd := "/system/routerboard/print"
	slog.Debug("SSH cmd is " + sshCmd)
	result, err := conn.Run(sshCmd)
	if err != nil {
		return updateNeeded, fmt.Errorf("failed to run SSH command: %w", err)
	}
	//result = "\r\n
	//	    routerboard: yes\r\n
	//            model: RB4011iGS+\r\n
	//         revision: r2\r\n
	//    serial-number: D4440DC29C87\r\n
	//    firmware-type: al2\r\n
	// factory-firmware: 6.45.9\r\n
	// current-firmware: 7.20.5\r\n
	// upgrade-firmware: 7.20.6\r\n\r\n"
	slog.Debug("SSH command result:" + result)

	// Check for installed version
	installedVersion, installedError := checkVersion(regexp.MustCompile(`.*current-firmware: (\S+)`), result)
	if installedError != nil {
		return updateNeeded, fmt.Errorf("failed to parse installed version: %v", installedError)
	}
	// Check for available version
	availableVersion, availableError := checkVersion(regexp.MustCompile(`.*upgrade-firmware: (\S+)`), result)
	if availableError != nil {
		return updateNeeded, fmt.Errorf("failed to parse available version: %v", availableError)
	}

	// Asses wether an update is needed or not
	if installedVersion == availableVersion {
		slog.Info("RouterBoard already up to date with RouterBoard " + installedVersion)
		fmt.Printf("✅ Router %v is up-to-date running RouterBoard %v\n", cfg.Host, installedVersion)
	} else {
		slog.Info("RouterBoard update available from RouterBoard version " + installedVersion + " to " + availableVersion)
		fmt.Printf("⚠️  Router %v can be upgraded from RouterBoard %v to %v\n", cfg.Host, installedVersion, availableVersion)
		updateNeeded = true
	}
	return updateNeeded, nil
}

func checkVersion(re *regexp.Regexp, output string) (string, error) {
	version := re.FindStringSubmatch(output)
	log.Printf("Version is %v", version)
	if len(version) < 2 {
		return "", errors.New("version not found in output")
	}

	return version[1], nil
}

func sshInit(cfg *core.Config) (*core.SshConnection, error) {
	// SSH init
	conn, err := core.NewSsh(fmt.Sprintf("%v:22", cfg.Host), cfg.User, cfg.Password)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

package export

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/urfave/cli/v3"
	"jb.favre/mikrotik-fleet-autopilot/common/core"
	"jb.favre/mikrotik-fleet-autopilot/common/display"
	"jb.favre/mikrotik-fleet-autopilot/common/ssh"
)

// ExportConfig holds all export configuration options
type ExportConfig struct {
	ShowSensitive      bool
	OutputDir          string
	Debug              bool
	MaxConcurrentHosts int
	PreferLiveMode     bool
}

// ExportDependencies holds injectable dependencies for testing
type ExportDependencies struct {
	SSHConnectionFactory func(context.Context, string) (ssh.RunnerInterface, error)
}

var Command = []*cli.Command{
	{
		Name:  "export",
		Usage: "Export MikroTik router configuration",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "show-sensitive",
				Value: false,
				Usage: "Include sensitive information in the export",
			},
			&cli.StringFlag{
				Name:  "output-dir",
				Value: ".",
				Usage: "Directory where to save the exported configuration",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			coreCfg, err := core.GetConfig(ctx)
			if err != nil {
				slog.Debug("failed to get global config", "error", err)
				return err
			}

			// Build export configuration from CLI flags
			exportCfg := ExportConfig{
				ShowSensitive:      cmd.Bool("show-sensitive"),
				OutputDir:          cmd.String("output-dir"),
				Debug:              coreCfg.Debug,
				MaxConcurrentHosts: coreCfg.EffectiveMaxConcurrent,
				PreferLiveMode:     coreCfg.PreferLiveMode,
			}

			deps := ExportDependencies{
				SSHConnectionFactory: ssh.CreateConnection,
			}

			return runExportForHosts(ctx, coreCfg.Hosts, exportCfg, deps, os.Stdout)
		},
	},
}

func runExportForHosts(ctx context.Context, hosts []string, cfg ExportConfig, deps ExportDependencies, out io.Writer) error {
	disp := display.New(out, hosts, display.InitOptions{
		Debug:          cfg.Debug,
		PreferLiveMode: cfg.PreferLiveMode,
		Concurrent:     cfg.MaxConcurrentHosts > 1,
	})
	core.SetLiveLogWriter(disp.LogWriter())

	// errs is indexed by host position so results are collected in host-list
	// order regardless of goroutine completion order.
	errs := make([]error, len(hosts))
	var wg sync.WaitGroup

	processHost := func(i int, host string) {
		line := disp.Line(i)
		displayStepCallback := display.NewStepCallback(line)
		filename, err := export(ctx, host, "", cfg, deps, displayStepCallback) // Empty preferred filename = derive automatically
		switch {
		case errors.Is(err, ssh.ErrConnectionFailed):
			line.CompleteStep("❓")
			line.Finish("❓", err.Error())
		case err != nil:
			line.CompleteStep("❌")
			line.FinishError(err.Error())
			errs[i] = err
		default:
			line.Finish("✅", fmt.Sprintf("Configuration exported to %s", filename))
		}
	}

	sem := make(chan struct{}, cfg.MaxConcurrentHosts)
	var ctxErr error
loop:
	for i, host := range hosts {
		if cfg.MaxConcurrentHosts <= 1 {
			processHost(i, host)
		} else {
			wg.Add(1)
			select {
			case sem <- struct{}{}:
				go func(idx int, h string) {
					defer wg.Done()
					defer func() { <-sem }()
					processHost(idx, h)
				}(i, host)
			case <-ctx.Done():
				wg.Done()
				ctxErr = ctx.Err()
				break loop
			}
		}
	}
	wg.Wait()
	result := errors.Join(append(errs, ctxErr)...)
	disp.Stop()
	core.SetLiveLogWriter(nil)
	return result
}

// Export is a public wrapper that exports configuration for a single host
// This function is intended to be called from other subcommands like enroll
func Export(ctx context.Context, host string, exportOutputDir string, exportShowSensitive bool, preferredFilename string) error {
	cfg := ExportConfig{
		ShowSensitive: exportShowSensitive,
		OutputDir:     exportOutputDir,
	}
	deps := ExportDependencies{
		SSHConnectionFactory: ssh.CreateConnection,
	}
	_, err := export(ctx, host, preferredFilename, cfg, deps, nil) // filename intentionally discarded; caller (enroll) manages output
	return err
}

func export(ctx context.Context, host string, preferredFilename string, cfg ExportConfig, deps ExportDependencies, displayStepCallback display.StepCallback) (string, error) {
	reportStep := func(icon, msg string) {
		if displayStepCallback != nil {
			displayStepCallback(icon, msg)
		}
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled: %w", err)
	}

	slog.Info("exporting configuration", "host", host)

	// Step 1: Establish SSH connection
	reportStep("⏳", "Connecting to router…")
	slog.Debug("initializing SSH connection", "host", host)
	conn, err := deps.SSHConnectionFactory(ctx, host)
	if err != nil {
		if errors.Is(err, ssh.ErrConnectionFailed) {
			slog.Warn("cannot connect to router", "host", host, "error", err)
			return "", err
		}
		slog.Error("failed to create SSH connection", "host", host, "error", err)
		return "", fmt.Errorf("failed to create SSH connection: %w", err)
	}
	reportStep("✅", "Connected")
	defer func() {
		_ = conn.Close()
	}()

	// Step 2: Execute export command
	sshCmd := "/export terse"
	if cfg.ShowSensitive {
		sshCmd += " show-sensitive"
	}
	reportStep("⏳", "Running export command…")
	slog.Debug("executing export command", "command", sshCmd, "show-sensitive", cfg.ShowSensitive)

	result, err := conn.Run(sshCmd)
	if err != nil {
		slog.Error("failed to export configuration", "host", host, "error", err)
		return "", fmt.Errorf("failed to export configuration: %w", err)
	}
	reportStep("✅", "Export command completed")

	// Step 3: Clean up Windows line endings (CRLF -> LF)
	reportStep("⏳", "Normalizing line endings…")
	result = strings.ReplaceAll(result, "\r\n", "\n")
	reportStep("✅", "Normalized output")

	// Step 4: Writing configuration to file
	var filename string
	if preferredFilename != "" {
		// Use provided name (from enroll's --hostname)
		filename = fmt.Sprintf("%s.rsc", preferredFilename)
	} else {
		// Derive from host using HostInfo
		hostInfo := ssh.ParseHost(host)
		filename = fmt.Sprintf("%s.rsc", hostInfo.ShortName)
	}
	outputPath := filepath.Join(cfg.OutputDir, filename)

	reportStep("⏳", "Writing configuration file…")
	slog.Debug("writing configuration", "file", outputPath, "size", len(result))
	if err := os.WriteFile(outputPath, []byte(result), 0644); err != nil {
		slog.Error("failed to write configuration file", "host", host, "file", outputPath, "error", err)
		return "", fmt.Errorf("failed to write configuration file: %w", err)
	}
	reportStep("✅", "Wrote configuration file")

	slog.Info("configuration exported successfully", "host", host, "file", outputPath)
	return filename, nil
}

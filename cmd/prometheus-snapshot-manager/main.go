package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/f18m/prometheus-snapshot-manager/internal/app"
	"github.com/f18m/prometheus-snapshot-manager/internal/config"
	"github.com/f18m/prometheus-snapshot-manager/internal/scheduler"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	cfgPath := "/etc/prometheus-snapshot-manager/config.yaml"
	logLevel := ""
	dryRun := false

	root := &cobra.Command{
		Use:   "prometheus-snapshot-manager",
		Short: "Prometheus snapshot daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(cmd.Context(), cfgPath, logLevel, dryRun)
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", cfgPath, "path to YAML config")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "override log level")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "simulate actions")

	root.AddCommand(&cobra.Command{Use: "run", RunE: func(cmd *cobra.Command, _ []string) error {
		return runDaemon(cmd.Context(), cfgPath, logLevel, dryRun)
	}})
	root.AddCommand(&cobra.Command{Use: "snapshot", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, logger, err := loadRuntime(cfgPath, logLevel)
		if err != nil {
			return err
		}
		logger.Info("running in snapshot mode")
		mgr, err := app.New(cmd.Context(), cfg, logger, dryRun)
		if err != nil {
			return err
		}
		return mgr.RunCycle(cmd.Context())
	}})
	root.AddCommand(&cobra.Command{Use: "prune", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, logger, err := loadRuntime(cfgPath, logLevel)
		if err != nil {
			return err
		}
		logger.Info("running in prune mode")
		mgr, err := app.New(cmd.Context(), cfg, logger, dryRun)
		if err != nil {
			return err
		}
		return mgr.Prune(cmd.Context())
	}})
	root.AddCommand(&cobra.Command{Use: "validate", RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := config.Load(cfgPath)
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "version", Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), version)
	}})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root.SetContext(ctx)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemon(ctx context.Context, cfgPath, logLevel string, dryRun bool) error {
	cfg, logger, err := loadRuntime(cfgPath, logLevel)
	if err != nil {
		return err
	}
	logger.Info("running in daemon mode")
	mgr, err := app.New(ctx, cfg, logger, dryRun)
	if err != nil {
		return err
	}
	return scheduler.Run(ctx,
		logger,
		cfg.Schedule.Timezone,
		cfg.Schedule.Cron,
		cfg.Schedule.Interval,
		cfg.Schedule.RunOnStartup,
		mgr)
}

func loadRuntime(path, overrideLevel string) (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	if overrideLevel != "" {
		cfg.Logging.Level = config.LogLevel(overrideLevel)
	}
	output := os.Stdout
	if cfg.Logging.Output != "" && cfg.Logging.Output != "stdout" {
		f, err := os.OpenFile(cfg.Logging.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		output = f
	}
	var level slog.Level
	switch cfg.Logging.Level {
	case config.LogLevelDebug:
		level = slog.LevelDebug
	case config.LogLevelWarn:
		level = slog.LevelWarn
	case config.LogLevelError:
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if cfg.Logging.Format == config.LogFormatText {
		h = slog.NewTextHandler(output, opts)
	} else {
		h = slog.NewJSONHandler(output, opts)
	}
	return cfg, slog.New(h), nil
}

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

type Runner interface {
	// RunCycle executes a single snapshot cycle, including snapshot
	// * creation,
	// * upload,
	// * retention pruning, and
	// * notifications.
	RunCycle(context.Context) error
}

// Run is the scheduler entry point. It supports running a task on startup, at a fixed interval, or based on a cron expression.
// timezone must be a valid *time.Location (already loaded and validated by config).
// interval is a pointer that may be nil; if nil, cron scheduling is used.
func Run(ctx context.Context, logger *slog.Logger, timezone *time.Location, cronExpr string, interval *time.Duration, runOnStartup bool, runner Runner) error {
	if runOnStartup {
		logger.Info("running initial snapshot cycle on startup")
		if err := runner.RunCycle(ctx); err != nil {
			return err
		}
	}

	// Either run with a fixed interval or with a cron expression
	if interval != nil {
		logger.Info("scheduling with fixed interval", "interval", interval.String())
		return runWithInterval(ctx, *interval, runner)
	} else {
		logger.Info("scheduling with cron expression", "cron", cronExpr, "timezone", timezone.String())
		return runWithCron(ctx, timezone, cronExpr, runner)
	}
}

func runWithInterval(ctx context.Context, interval time.Duration, runner Runner) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := runner.RunCycle(ctx); err != nil {
				return err
			}
		}
	}
}

func runWithCron(ctx context.Context, timezone *time.Location, cronExpr string, runner Runner) error {
	// setup cron parser and scheduler
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	c := cron.New(cron.WithLocation(timezone), cron.WithParser(parser))
	if _, err := c.AddFunc(cronExpr, func() { _ = runner.RunCycle(ctx) }); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	// start cron scheduler
	c.Start()
	defer c.Stop()

	// wait for context cancellation
	<-ctx.Done()
	return nil
}

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
		return runWithInterval(ctx, logger, *interval, runner)
	} else {
		return runWithCron(ctx, logger, timezone, cronExpr, runner)
	}
}

func runWithInterval(ctx context.Context, logger *slog.Logger, interval time.Duration, runner Runner) error {
	logger.Info("next cycle scheduled", "in", interval.String())
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
			logger.Info("next cycle scheduled", "in", interval.String())
		}
	}
}

func runWithCron(ctx context.Context, logger *slog.Logger, timezone *time.Location, cronExpr string, runner Runner) error {
	// setup cron parser and scheduler
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	c := cron.New(cron.WithLocation(timezone), cron.WithParser(parser))
	var entryID cron.EntryID
	var err error
	entryID, err = c.AddFunc(cronExpr, func() {
		if runErr := runner.RunCycle(ctx); runErr != nil {
			logger.Error("scheduled cycle failed", "error", runErr)
			return
		}
		next := c.Entry(entryID).Next
		if !next.IsZero() {
			logger.Info("next cycle scheduled", "in", time.Until(next).Round(time.Second).String(), "at", next.Format(time.RFC3339))
		}
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	logger.Info("scheduling with cron expression", "cron", cronExpr, "timezone", timezone.String())

	// start cron scheduler
	c.Start()
	defer c.Stop()
	next := c.Entry(entryID).Next
	if !next.IsZero() {
		logger.Info("next cycle scheduled", "in", time.Until(next).Round(time.Second).String(), "at", next.Format(time.RFC3339))
	}

	// wait for context cancellation
	<-ctx.Done()
	return nil
}

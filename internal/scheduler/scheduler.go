package scheduler

import (
	"context"
	"fmt"
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
func Run(ctx context.Context, timezone *time.Location, cronExpr string, interval *time.Duration, runOnStartup bool, runner Runner) error {
	if runOnStartup {
		if err := runner.RunCycle(ctx); err != nil {
			return err
		}
	}
	if interval != nil {
		t := time.NewTicker(*interval)
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
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	c := cron.New(cron.WithLocation(timezone), cron.WithParser(parser))
	if _, err := c.AddFunc(cronExpr, func() { _ = runner.RunCycle(ctx) }); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	c.Start()
	defer c.Stop()
	<-ctx.Done()
	return nil
}

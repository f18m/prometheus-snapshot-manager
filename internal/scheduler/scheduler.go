package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

type Runner interface {
	RunCycle(context.Context) error
}

func Run(ctx context.Context, timezone, cronExpr, interval string, runOnStartup bool, runner Runner) error {
	if runOnStartup {
		if err := runner.RunCycle(ctx); err != nil {
			return err
		}
	}
	if interval != "" {
		d, err := time.ParseDuration(interval)
		if err != nil {
			return err
		}
		t := time.NewTicker(d)
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
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	c := cron.New(cron.WithLocation(loc), cron.WithParser(parser))
	if _, err := c.AddFunc(cronExpr, func() { _ = runner.RunCycle(ctx) }); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	c.Start()
	defer c.Stop()
	<-ctx.Done()
	return nil
}

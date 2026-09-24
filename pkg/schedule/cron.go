package schedule

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrCronRequired    = errors.New("cron is required for type=cron")
	ErrNextTimeMissing = errors.New("NextTime not configured")
	ErrInvalidCronExpr = errors.New("invalid cron expression")
)

func ApplyCron(sc *Schedule, cron string, now time.Time, nextTime func(string, time.Time) (time.Time, error)) error {
	cron = strings.TrimSpace(cron)
	if cron == "" {
		return ErrCronRequired
	}
	if nextTime == nil {
		return ErrNextTimeMissing
	}
	nextRunAt, err := nextTime(cron, now)
	if err != nil {
		return ErrInvalidCronExpr
	}
	sc.ScheduleType = "cron"
	sc.CronExpr = cron
	sc.NextRunAt = nextRunAt
	return nil
}

package retention

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const ArchiveTimeLayout = "2006-01-02T150405Z"

type Policy struct {
	KeepWithin  time.Duration
	KeepMinimum int
	KeepMaximum int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
}

type SnapshotFile struct {
	Name      string
	Timestamp time.Time
}

type Decision struct {
	KeepReasons map[string][]string
	Delete      []SnapshotFile
}

func ParseArchiveTimestamp(name string) (time.Time, error) {
	if !strings.HasPrefix(name, "prom-snapshot_") || !strings.HasSuffix(name, ".tar.gz") {
		return time.Time{}, fmt.Errorf("invalid archive filename: %s", name)
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, "prom-snapshot_"), ".tar.gz")
	parts := strings.Split(base, "_")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid archive filename: %s", name)
	}
	return time.Parse(ArchiveTimeLayout, parts[0])
}

func Evaluate(policy Policy, files []SnapshotFile, now time.Time) Decision {
	sorted := append([]SnapshotFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.After(sorted[j].Timestamp)
	})

	reasons := map[string][]string{}
	keep := map[string]bool{}

	mark := func(f SnapshotFile, reason string) {
		keep[f.Name] = true
		reasons[f.Name] = append(reasons[f.Name], reason)
	}

	if policy.KeepWithin > 0 {
		cutoff := now.Add(-policy.KeepWithin)
		for _, f := range sorted {
			if f.Timestamp.After(cutoff) {
				mark(f, "keep_within")
			}
		}
	}

	if policy.KeepMinimum > 0 {
		for i := 0; i < len(sorted) && i < policy.KeepMinimum; i++ {
			mark(sorted[i], "keep_minimum")
		}
	}

	if policy.KeepDaily > 0 {
		dayBuckets := map[string]SnapshotFile{}
		for _, f := range sorted {
			daysAgo := int(now.Sub(f.Timestamp).Hours() / 24)
			if daysAgo < 0 || daysAgo >= policy.KeepDaily {
				continue
			}
			k := f.Timestamp.UTC().Format("2006-01-02")
			if _, ok := dayBuckets[k]; !ok {
				dayBuckets[k] = f
			}
		}
		for _, f := range dayBuckets {
			mark(f, "keep_daily")
		}
	}

	if policy.KeepWeekly > 0 {
		weekBuckets := map[string]SnapshotFile{}
		nowYear, nowWeek := now.ISOWeek()
		for _, f := range sorted {
			y, w := f.Timestamp.ISOWeek()
			weeksAgo := (nowYear-y)*53 + (nowWeek - w)
			if weeksAgo < 0 || weeksAgo >= policy.KeepWeekly {
				continue
			}
			k := fmt.Sprintf("%d-W%02d", y, w)
			if _, ok := weekBuckets[k]; !ok {
				weekBuckets[k] = f
			}
		}
		for _, f := range weekBuckets {
			mark(f, "keep_weekly")
		}
	}

	if policy.KeepMonthly > 0 {
		monthBuckets := map[string]SnapshotFile{}
		nowMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		for _, f := range sorted {
			fMonthStart := time.Date(f.Timestamp.Year(), f.Timestamp.Month(), 1, 0, 0, 0, 0, time.UTC)
			monthsAgo := (nowMonthStart.Year()-fMonthStart.Year())*12 + int(nowMonthStart.Month()-fMonthStart.Month())
			if monthsAgo < 0 || monthsAgo >= policy.KeepMonthly {
				continue
			}
			k := fMonthStart.Format("2006-01")
			if _, ok := monthBuckets[k]; !ok {
				monthBuckets[k] = f
			}
		}
		for _, f := range monthBuckets {
			mark(f, "keep_monthly")
		}
	}

	kept := make([]SnapshotFile, 0, len(sorted))
	for _, f := range sorted {
		if keep[f.Name] {
			kept = append(kept, f)
		}
	}
	if policy.KeepMaximum > 0 && len(kept) > policy.KeepMaximum {
		for i := policy.KeepMaximum; i < len(kept); i++ {
			delete(keep, kept[i].Name)
			reasons[kept[i].Name] = append(reasons[kept[i].Name], "exceeds_keep_maximum")
		}
	}

	var del []SnapshotFile
	for _, f := range sorted {
		if !keep[f.Name] {
			del = append(del, f)
		}
	}
	return Decision{KeepReasons: reasons, Delete: del}
}

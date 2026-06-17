package retention

import (
	"testing"
	"time"
)

func TestParseArchiveTimestamp(t *testing.T) {
	ts, err := ParseArchiveTimestamp("prom-snapshot_2025-06-17T020000Z_a3f9b2.tar.gz")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ts.UTC().Format(ArchiveTimeLayout) != "2025-06-17T020000Z" {
		t.Fatalf("unexpected timestamp: %s", ts)
	}
}

func TestEvaluatePolicy(t *testing.T) {
	now := time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC)
	files := []SnapshotFile{
		{Name: "a", Timestamp: now.Add(-1 * time.Hour)},
		{Name: "b", Timestamp: now.Add(-26 * time.Hour)},
		{Name: "c", Timestamp: now.Add(-8 * 24 * time.Hour)},
		{Name: "d", Timestamp: now.Add(-35 * 24 * time.Hour)},
	}
	p := Policy{KeepWithin: 24 * time.Hour, KeepMinimum: 2, KeepDaily: 2, KeepWeekly: 2, KeepMonthly: 2, KeepMaximum: 3}
	d := Evaluate(p, files, now)
	if len(d.Delete) != 1 {
		t.Fatalf("expected 1 delete, got %d", len(d.Delete))
	}
	if d.Delete[0].Name != "d" {
		t.Fatalf("expected delete d, got %s", d.Delete[0].Name)
	}
}

package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/f18m/prometheus-snapshot-manager/internal/config"
	"github.com/f18m/prometheus-snapshot-manager/internal/notify"
	"github.com/f18m/prometheus-snapshot-manager/internal/retention"
	"github.com/f18m/prometheus-snapshot-manager/internal/snapshot_api"
	"github.com/f18m/prometheus-snapshot-manager/internal/target"
	"github.com/f18m/prometheus-snapshot-manager/internal/utils"
)

// Manager is the core orchestrator of the prometheus-snapshot-manager application.
// It coordinates the complete lifecycle of snapshot creation, archiving, distribution,
// and retention across multiple backup targets.
//
// Architecture and Responsibilities:
//   - Creates snapshots from a remote Prometheus server via its HTTP API
//   - Compresses snapshots into archives (tar.gz, tar.bz2, etc.)
//   - Distributes archives to multiple backup targets in parallel (local filesystem, SFTP, S3)
//   - Implements retention policies to manage backup storage (keeps minimum/maximum snapshots, enforces time windows)
//   - Sends notifications via Apprise (email, Slack, Discord, etc.) on success or failure
//
// Key Design Patterns:
//   - Concurrent uploads: Uses goroutines + sync.WaitGroup to upload to all targets in parallel
//   - Error aggregation: Collects errors from concurrent uploads and reports as a single failure
//   - Context propagation: Respects context cancellation and timeouts throughout the cycle
//   - Dry-run mode: Allows validating configuration without modifying production data
type Manager struct {
	cfg      *config.Config
	logger   *slog.Logger
	dryRun   bool
	targets  []target.Target
	notifier *notify.Notifier
}

func New(ctx context.Context, cfg *config.Config, logger *slog.Logger, dryRun bool) (*Manager, error) {
	targets := make([]target.Target, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		switch t.Type {
		case "local":
			targets = append(targets, target.NewLocalTarget(t.Name, t.Local.Path))
		case "sftp":
			targets = append(targets, target.NewSFTPTarget(t.Name, t.SFTP))
		case "s3":
			s3t, err := target.NewS3Target(ctx, t.Name, t.S3)
			if err != nil {
				return nil, err
			}
			targets = append(targets, s3t)
		}
	}
	return &Manager{
		cfg:      cfg,
		logger:   logger,
		dryRun:   dryRun,
		targets:  targets,
		notifier: notify.New(cfg.Notifications.Apprise),
	}, nil
}

func (m *Manager) RunCycle(ctx context.Context) (retErr error) {
	start := time.Now()
	timeout := m.cfg.Prometheus.Timeout

	client := snapshot_api.NewClient(
		m.cfg.Prometheus.URL,
		timeout,
		m.cfg.Prometheus.BasicAuth.Username,
		m.cfg.Prometheus.BasicAuth.Password,
		m.cfg.Prometheus.TLSSkipVerify,
	)

	snapshotName := ""
	defer func() {
		status := "success"
		errText := ""
		if retErr != nil {
			status = "failure"
			errText = retErr.Error()
		}
		_ = m.notifier.Send(ctx, notify.Payload{
			Status:       status,
			SnapshotName: snapshotName,
			Duration:     time.Since(start),
			Targets:      m.targetNames(),
			Error:        errText,
		})
	}()

	// create snapshot

	startupWait := *m.cfg.Prometheus.StartupWait
	if startupWait > 0 {
		if err := client.WaitUntilReady(ctx, startupWait, m.logger); err != nil {
			return err
		}
	}

	name, err := client.CreateSnapshotWithRetry(ctx, startupWait, m.logger)
	if err != nil {
		return err
	}

	snapshotName = name
	m.logger.Info("snapshot created", "name", name)

	// from now on the raw snapshot dir exists (or is about to): remove it on every exit path, not only on success
	snapshotPath := filepath.Join(m.cfg.Prometheus.SnapshotDir, name)
	defer func() {
		if err := m.cleanupSnapshotDir(snapshotPath); err != nil {
			m.logger.Warn("failed to cleanup snapshot dir", "path", snapshotPath, "error", err)
			if retErr == nil {
				retErr = err
			}
		}
	}()

	snapCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := client.WaitForSnapshotDirReady(snapCtx, m.cfg.Prometheus.SnapshotDir, name, 500*time.Millisecond); err != nil {
		return err
	}

	m.logger.Info("snapshot directory ready", "path", snapshotPath)

	// archive snapshot

	diskSnapshotter := snapshot_api.NewDiskSnapshotter(snapshotPath)

	archiveName, err := diskSnapshotter.ArchiveFilename(time.Now().UTC())
	if err != nil {
		return err
	}
	archivePath := filepath.Join(m.cfg.Prometheus.SnapshotArchiveTempDir, snapshotName+"-"+archiveName)
	archiveStart := time.Now()
	archiveSize, err := diskSnapshotter.BuildArchiveToFile(archivePath, m.cfg.Compression.Level)
	if err != nil {
		return err
	}
	archiveDuration := time.Since(archiveStart)
	defer func() {
		if rmErr := os.Remove(archivePath); rmErr != nil && !os.IsNotExist(rmErr) {
			m.logger.Warn("failed to cleanup local archive file", "path", archivePath, "error", rmErr)
		} else {
			m.logger.Info("local archive file successfully cleaned up", "path", archivePath)
		}
	}()

	m.logger.Info("snapshot archive created", "path", archivePath, "size", utils.FormatBytesSI(archiveSize), "duration_seconds", archiveDuration.Seconds())

	// upload to targets

	if err := m.uploadAll(ctx, archiveName, archivePath, archiveSize); err != nil {
		return err
	}

	m.logger.Info("snapshot archive uploaded", "name", archiveName)

	// prune old snapshots

	if err := m.Prune(ctx); err != nil {
		return err
	}

	return nil
}

// cleanupSnapshotDir removes the raw Prometheus snapshot directory of a cycle, if enabled.
// Snapshots are hardlinks to the TSDB blocks, but they must be removed anyway:
//   - once Prometheus compacts or retention-deletes blocks, the hardlinks keep the deleted blocks on disk,
//     so the data volume grows beyond the size of the TSDB and --storage.tsdb.retention.size does not see it;
//   - the archive uploaded to the targets is the actual backup: the raw snapshot has no further use;
//   - if the hardlinks cannot be created (e.g. snapshot dir on another filesystem), each snapshot is a full copy of the TSDB.
func (m *Manager) cleanupSnapshotDir(snapshotPath string) error {
	if !*m.cfg.Retention.CleanupPrometheusSnapshots {
		return nil
	}
	if m.dryRun {
		m.logger.Info("dry-run cleanup snapshot dir", "path", snapshotPath)
		return nil
	}
	if err := os.RemoveAll(snapshotPath); err != nil {
		return err
	}
	m.logger.Info("successfully cleaned up snapshot dir", "path", snapshotPath)
	return nil
}

// CleanupArchiveTempDir removes the temporary archives left in the archive temp directory by previous runs that
// were interrupted before removing them (e.g. the process was killed while archiving or uploading).
// It must only be called at daemon startup, when no cycle of this process is in progress. Only regular files whose
// name could have been written by this application are removed: anything else in the directory is left untouched.
func (m *Manager) CleanupArchiveTempDir() error {
	dir := m.cfg.Prometheus.SnapshotArchiveTempDir
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !snapshot_api.IsTempArchiveName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if m.dryRun {
			m.logger.Info("dry-run cleanup stale archive in archive temp dir", "path", path)
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		m.logger.Info("removed stale archive in archive temp dir", "path", path)
	}
	return nil
}

func (m *Manager) Prune(ctx context.Context) error {
	keepWithin := time.Duration(0)
	if m.cfg.Retention.KeepWithin != nil {
		keepWithin = *m.cfg.Retention.KeepWithin
	}

	policy := retention.Policy{
		KeepWithin:  keepWithin,
		KeepMinimum: m.cfg.Retention.KeepMinimum,
		KeepMaximum: m.cfg.Retention.KeepMaximum,
		KeepDaily:   m.cfg.Retention.KeepDaily,
		KeepWeekly:  m.cfg.Retention.KeepWeekly,
		KeepMonthly: m.cfg.Retention.KeepMonthly,
	}

	for _, t := range m.targets {
		files, err := t.List(ctx)
		if err != nil {
			return err
		}
		items := make([]retention.SnapshotFile, 0, len(files))
		for _, f := range files {
			items = append(items, retention.SnapshotFile{Name: f.Name, Timestamp: f.Timestamp})
		}
		decision := retention.Evaluate(policy, items, time.Now().UTC())

		reasonKeys := make([]string, 0, len(decision.KeepReasons))
		for k := range decision.KeepReasons {
			reasonKeys = append(reasonKeys, k)
		}
		sort.Strings(reasonKeys)
		for _, file := range reasonKeys {
			m.logger.Info("retention keep", "target", t.Name(), "file", file, "reasons", strings.Join(decision.KeepReasons[file], ","))
		}
		for _, del := range decision.Delete {
			if m.dryRun {
				m.logger.Info("dry-run retention delete", "target", t.Name(), "file", del.Name)
				continue
			}
			m.logger.Info("retention delete", "target", t.Name(), "file", del.Name)
			if err := t.Delete(ctx, del.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) uploadAll(ctx context.Context, archiveName, archivePath string, archiveSize int64) error {
	uploadStart := time.Now()
	if m.dryRun {
		for _, t := range m.targets {
			m.logger.Info("dry-run upload", "target", t.Name(), "file", archiveName, "size", utils.FormatBytesSI(archiveSize))
		}
		return nil
	}

	type uploadErr struct {
		target string
		err    error
	}
	ch := make(chan uploadErr, len(m.targets))
	var wg sync.WaitGroup

	for _, t := range m.targets {
		t := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := os.Open(filepath.Clean(archivePath))
			if err != nil {
				ch <- uploadErr{target: t.Name(), err: err}
				return
			}
			defer f.Close()

			if err := t.Upload(ctx, m.logger, archiveName, f); err != nil {
				ch <- uploadErr{target: t.Name(), err: err}
				return
			}
		}()
	}

	wg.Wait()
	close(ch)
	var errs []string
	for e := range ch {
		errStr := fmt.Sprintf("%s: %v", e.target, e.err)
		errs = append(errs, errStr)
	}
	if len(errs) > 0 {
		return fmt.Errorf("upload failed: %s", strings.Join(errs, "; "))
	} else {
		m.logger.Info("all uploads completed successfully", "targets", m.targetNames(), "duration_seconds", time.Since(uploadStart).Seconds())
	}
	return nil
}

func (m *Manager) targetNames() string {
	names := make([]string, 0, len(m.targets))
	for _, t := range m.targets {
		names = append(names, t.Name())
	}
	return strings.Join(names, ",")
}

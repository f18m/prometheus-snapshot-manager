# prometheus-snapshot-manager

[![CI](https://github.com/f18m/prometheus-snapshot-manager/actions/workflows/ci.yaml/badge.svg)](https://github.com/f18m/prometheus-snapshot-manager/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/f18m/prometheus-snapshot-manager)](https://github.com/f18m/prometheus-snapshot-manager/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/f18m/prometheus-snapshot-manager)](./go.mod)
[![License](https://img.shields.io/github/license/f18m/prometheus-snapshot-manager)](./LICENSE)

A production-ready headless daemon/CLI that periodically triggers Prometheus TSDB snapshots, archives them as `tar.gz`, uploads to one or more backup targets (local/SFTP/S3-compatible), applies retention pruning, and sends success/failure notifications through Apprise.

## Comparison with prombackup

`prombackup` focuses on exposing backup content and workflows through an HTTP interface. `prometheus-snapshot-manager` is intentionally upload-centric and scheduler-driven: no web server, no UI, no download endpoint—just snapshot, archive, upload, prune, notify.

## Quick start

Use the included `docker-compose.example.yml`, or this minimal snippet:

```yaml
services:
  prometheus-snapshot-manager:
    image: ghcr.io/f18m/prometheus-snapshot-manager:latest
    volumes:
      - prometheus_data:/prometheus:ro
      - ./config.yaml:/etc/prometheus-snapshot-manager/config.yaml:ro
      - /backup/prometheus:/backup/prometheus
    command: ["run"]
```

## Configuration reference

| Field | Type | Default | Description |
|---|---|---|---|
| `prometheus.url` | string | - | Prometheus base URL |
| `prometheus.timeout` | duration | `30s` | API and wait timeout |
| `prometheus.startup_wait` | duration | `5m` | Max time to wait (readiness poll on `/-/ready` plus retries of transient errors such as HTTP 503 or connection refused, with exponential backoff up to 30s) for Prometheus before failing a cycle; `0` disables waiting |
| `prometheus.snapshot_dir` | string | - | Filesystem snapshot root |
| `prometheus.snapshot_archive_temp_dir` | string | `/prometheus/snapshots-mgr-temp` | Temporary directory used to build archive files before upload (see `cleanup_archive_temp_dir_on_startup`) |
| `prometheus.cleanup_archive_temp_dir_on_startup` | bool | `true` | Delete the temporary archives left in `snapshot_archive_temp_dir` by interrupted runs when the daemon starts. Only regular files named like the archives this application writes are removed. Not applied to the one-shot `snapshot`/`prune` commands, which may run next to a live daemon |
| `prometheus.tls_skip_verify` | bool | `false` | Skip TLS verification |
| `prometheus.basic_auth.username` | string | `""` | Optional basic auth username |
| `prometheus.basic_auth.password` | string | `""` | Optional basic auth password |
| `schedule.cron` | string | empty | Cron expression (supports seconds) |
| `schedule.interval` | duration | empty | Alternative fixed interval |
| `schedule.timezone` | string | `UTC` | Scheduler timezone |
| `schedule.run_on_startup` | bool | `false` | Run once at startup |
| `compression.format` | string | `tar.gz` | Archive format (v1 only) |
| `compression.level` | int | `6` | Gzip level `1-9` |
| `retention.keep_within` | duration | empty | Keep files newer than now-duration |
| `retention.keep_minimum` | int | `0` | Always keep newest N |
| `retention.keep_maximum` | int | `0` | Keep at most N after all rules |
| `retention.keep_daily` | int | `0` | Keep latest snapshot/day for N days |
| `retention.keep_weekly` | int | `0` | Keep latest snapshot/week for N ISO weeks |
| `retention.keep_monthly` | int | `0` | Keep latest snapshot/month for N months |
| `retention.cleanup_prometheus_snapshots` | bool | `true` | Remove the raw snapshot dir at the end of each cycle, on success and on failure (see [Prometheus data volume](#prometheus-data-volume)) |
| `targets[].name` | string | - | Target name |
| `targets[].type` | string | - | One of `local`, `sftp`, `s3` |
| `targets[].local.path` | string | - | Local directory path |
| `targets[].sftp.*` | object | - | SFTP connection/auth options |
| `targets[].s3.*` | object | - | S3 bucket/credentials/options |
| `notifications.apprise.enabled` | bool | `false` | Enable notifications |
| `notifications.apprise.config_file` | string | empty | Apprise config path |
| `notifications.apprise.urls[]` | list | empty | Inline Apprise URLs |
| `notifications.apprise.on_success` | bool | `true` | Notify on success |
| `notifications.apprise.on_failure` | bool | `true` | Notify on failure |
| `notifications.apprise.title_template` | string | built-in | Go template for title |
| `notifications.apprise.body_template` | string | built-in | Go template for body |
| `logging.level` | string | `info` | `debug/info/warn/error` |
| `logging.format` | string | `json` | `json` or `text` |
| `logging.output` | string | `stdout` | `stdout` or file path |

More detailed documentation for the configuration in the form
of YAML comments in [config.docs.yaml](./config.docs.yaml)

## Secret management

Any secret field can use:
- `${ENV_VAR_NAME}` to read from environment
- `file:/path/to/secret` to read from file and trim whitespace

## Retention policy

Archive naming format:

```text
prom-snapshot_<RFC3339-UTC-timestamp>_<random-6-chars>.tar.gz
```

Rules are applied in order and a snapshot is kept if any keep rule matches:
1. `keep_within`
2. `keep_minimum`
3. `keep_daily`
4. `keep_weekly`
5. `keep_monthly`
6. `keep_maximum` cap after all keeps

## Prometheus data volume

With the default paths, the raw snapshots (`prometheus.snapshot_dir`) and the temporary archives (`prometheus.snapshot_archive_temp_dir`) live inside the Prometheus data volume, next to the TSDB. They are not covered by `--storage.tsdb.retention.size` and can grow larger than the TSDB itself if they are not removed.

To avoid that, the manager:
- removes the raw snapshot directory at the end of every cycle, on success and on failure (`retention.cleanup_prometheus_snapshots: true`, the default);
- removes the temporary archive as soon as the cycle ends, on success and on failure;
- at daemon startup, removes the leftovers of archives that a previous run could not remove (`prometheus.cleanup_archive_temp_dir_on_startup: true`, the default).

Raw snapshots are deleted even though they are made of hardlinks to the TSDB blocks, for these reasons:
- once Prometheus compacts or retention-deletes a block, the snapshot hardlinks keep the deleted block on disk, so the volume grows beyond the TSDB size and `--storage.tsdb.retention.size` does not account for it;
- the uploaded archive is the backup: the raw snapshot has no further use once the archive is built;
- hardlinks only work on the same filesystem. If the snapshot directory is on a different mount, each snapshot is a full copy of the TSDB.

Not everything is covered automatically: if the process is killed with `SIGKILL` (or the host crashes) during a cycle, the raw snapshot directory of that cycle stays in `prometheus.snapshot_dir`. Such leftovers must be removed manually while the manager is stopped.

`prometheus.cleanup_archive_temp_dir_on_startup` only deletes regular files whose names match the temporary archives written by this application (`<Prometheus snapshot name>-prom-snapshot_<timestamp>_<random>.tar.gz`). Any other file or directory in `prometheus.snapshot_archive_temp_dir` is left untouched.

## Notification setup

Notifications use the Apprise CLI. See https://github.com/caronc/apprise for URL formats and advanced config.

Minimal example:

```yaml
notifications:
  apprise:
    enabled: true
    urls:
      - "tgram://bottoken/chatid"
```

## Building from source

```bash
go mod tidy
go vet ./...
go test ./...
go build ./cmd/prometheus-snapshot-manager
```

Or using [Just](https://github.com/casey/just):

```bash
just build
```

## Contributing

Issues and PRs are welcome. Please include tests for behavior changes, keep changes focused, and ensure `go vet`, `go test`, and `go build` all pass.

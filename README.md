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
| `prometheus.snapshot_dir` | string | - | Filesystem snapshot root |
| `prometheus.snapshot_archive_temp_dir` | string | `/prometheus/snapshots-mgr-temp` | Temporary directory used to build archive files before upload |
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
| `retention.cleanup_prometheus_snapshots` | bool | `false` | Remove raw snapshot dir after success |
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

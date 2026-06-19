package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Prometheus    PrometheusConfig   `yaml:"prometheus"`
	Schedule      ScheduleConfig     `yaml:"schedule"`
	Compression   CompressionConfig  `yaml:"compression"`
	Retention     RetentionConfig    `yaml:"retention"`
	Targets       []TargetConfig     `yaml:"targets"`
	Notifications NotificationConfig `yaml:"notifications"`
	Logging       LoggingConfig      `yaml:"logging"`
}

type PrometheusConfig struct {
	URL           string        `yaml:"url"`
	Timeout       string        `yaml:"timeout"`
	SnapshotDir   string        `yaml:"snapshot_dir"`
	TLSSkipVerify bool          `yaml:"tls_skip_verify"`
	BasicAuth     PromBasicAuth `yaml:"basic_auth"`
}

type PromBasicAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type ScheduleConfig struct {
	Cron         string `yaml:"cron"`
	Interval     string `yaml:"interval"`
	Timezone     string `yaml:"timezone"`
	RunOnStartup bool   `yaml:"run_on_startup"`
}

type CompressionConfig struct {
	Format string `yaml:"format"`
	Level  int    `yaml:"level"`
}

type RetentionConfig struct {
	KeepWithin                 string `yaml:"keep_within"`
	KeepMinimum                int    `yaml:"keep_minimum"`
	KeepMaximum                int    `yaml:"keep_maximum"`
	KeepDaily                  int    `yaml:"keep_daily"`
	KeepWeekly                 int    `yaml:"keep_weekly"`
	KeepMonthly                int    `yaml:"keep_monthly"`
	CleanupPrometheusSnapshots bool   `yaml:"cleanup_prometheus_snapshots"`
}

type TargetConfig struct {
	Name  string      `yaml:"name"`
	Type  string      `yaml:"type"`
	Local LocalConfig `yaml:"local"`
	SFTP  SFTPConfig  `yaml:"sftp"`
	S3    S3Config    `yaml:"s3"`
}

type LocalConfig struct {
	Path string `yaml:"path"`
}

type SFTPConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	User           string `yaml:"user"`
	Password       string `yaml:"password"`
	KeyFile        string `yaml:"key_file"`
	KeyData        string `yaml:"key_data"`
	RemotePath     string `yaml:"remote_path"`
	HostKeyCheck   bool   `yaml:"host_key_check"`
	KnownHostsFile string `yaml:"known_hosts_file"`
}

type S3Config struct {
	Endpoint        string `yaml:"endpoint"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	Prefix          string `yaml:"prefix"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	StorageClass    string `yaml:"storage_class"`
	ForcePathStyle  bool   `yaml:"force_path_style"`
}

type NotificationConfig struct {
	Apprise AppriseConfig `yaml:"apprise"`
}

type AppriseConfig struct {
	Enabled       bool     `yaml:"enabled"`
	ConfigFile    string   `yaml:"config_file"`
	URLs          []string `yaml:"urls"`
	OnSuccess     *bool    `yaml:"on_success"`
	OnFailure     *bool    `yaml:"on_failure"`
	TitleTemplate string   `yaml:"title_template"`
	BodyTemplate  string   `yaml:"body_template"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Prometheus.Timeout == "" {
		c.Prometheus.Timeout = "30s"
	}
	if c.Compression.Format == "" {
		c.Compression.Format = "tar.gz"
	}
	if c.Compression.Level == 0 {
		c.Compression.Level = 6
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Logging.Output == "" {
		c.Logging.Output = "stdout"
	}
	if c.Schedule.Timezone == "" {
		c.Schedule.Timezone = "UTC"
	}
	if c.Notifications.Apprise.TitleTemplate == "" {
		c.Notifications.Apprise.TitleTemplate = "[prometheus-snapshot-manager] {{ .Status }}: {{ .SnapshotName }}"
	}
	if c.Notifications.Apprise.BodyTemplate == "" {
		c.Notifications.Apprise.BodyTemplate = "Status: {{ .Status }}\nSnapshot: {{ .SnapshotName }}\nError: {{ .Error }}"
	}
}

func (c *Config) resolveSecrets() error {
	var err error
	c.Prometheus.BasicAuth.Password, err = resolveSecret(c.Prometheus.BasicAuth.Password)
	if err != nil {
		return fmt.Errorf("resolve prometheus.basic_auth.password: %w", err)
	}

	for i := range c.Targets {
		t := &c.Targets[i]
		t.SFTP.Password, err = resolveSecret(t.SFTP.Password)
		if err != nil {
			return fmt.Errorf("resolve targets[%d].sftp.password: %w", i, err)
		}
		t.SFTP.KeyData, err = resolveSecret(t.SFTP.KeyData)
		if err != nil {
			return fmt.Errorf("resolve targets[%d].sftp.key_data: %w", i, err)
		}
		t.S3.AccessKeyID, err = resolveSecret(t.S3.AccessKeyID)
		if err != nil {
			return fmt.Errorf("resolve targets[%d].s3.access_key_id: %w", i, err)
		}
		t.S3.SecretAccessKey, err = resolveSecret(t.S3.SecretAccessKey)
		if err != nil {
			return fmt.Errorf("resolve targets[%d].s3.secret_access_key: %w", i, err)
		}
	}

	for i := range c.Notifications.Apprise.URLs {
		c.Notifications.Apprise.URLs[i], err = resolveSecret(c.Notifications.Apprise.URLs[i])
		if err != nil {
			return fmt.Errorf("resolve notifications.apprise.urls[%d]: %w", i, err)
		}
	}

	return nil
}

func resolveSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		k := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
		if k == "" {
			return "", errors.New("empty env var name")
		}
		v, ok := os.LookupEnv(k)
		if !ok {
			return "", fmt.Errorf("env var %q not set", k)
		}
		return v, nil
	}
	if strings.HasPrefix(value, "file:") {
		path := strings.TrimPrefix(value, "file:")
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return value, nil
}

func (c *Config) Validate() error {
	if c.Prometheus.URL == "" {
		return errors.New("prometheus.url is required")
	}
	if c.Prometheus.SnapshotDir == "" {
		return errors.New("prometheus.snapshot_dir is required")
	}
	if _, err := time.ParseDuration(c.Prometheus.Timeout); err != nil {
		return fmt.Errorf("invalid prometheus.timeout: %w", err)
	}
	if c.Schedule.Cron == "" && c.Schedule.Interval == "" {
		return errors.New("either schedule.cron or schedule.interval is required")
	}
	if c.Schedule.Interval != "" {
		if _, err := time.ParseDuration(c.Schedule.Interval); err != nil {
			return fmt.Errorf("invalid schedule.interval: %w", err)
		}
	}
	if _, err := time.LoadLocation(c.Schedule.Timezone); err != nil {
		return fmt.Errorf("invalid schedule.timezone: %w", err)
	}
	if c.Compression.Format != "tar.gz" {
		return errors.New("compression.format must be tar.gz")
	}
	if c.Compression.Level < 1 || c.Compression.Level > 9 {
		return errors.New("compression.level must be between 1 and 9")
	}
	if len(c.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	for i, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		switch t.Type {
		case "local":
			if t.Local.Path == "" {
				return fmt.Errorf("targets[%d].local.path is required", i)
			}
		case "sftp":
			if t.SFTP.Host == "" || t.SFTP.User == "" || t.SFTP.RemotePath == "" {
				return fmt.Errorf("targets[%d].sftp host/user/remote_path are required", i)
			}
			if t.SFTP.Port == 0 {
				c.Targets[i].SFTP.Port = 22
			}
		case "s3":
			if t.S3.Region == "" || t.S3.Bucket == "" {
				return fmt.Errorf("targets[%d].s3 region/bucket are required", i)
			}
		default:
			return fmt.Errorf("targets[%d].type unsupported: %s", i, t.Type)
		}
	}
	if c.Retention.KeepWithin != "" {
		if _, err := time.ParseDuration(c.Retention.KeepWithin); err != nil {
			return fmt.Errorf("invalid retention.keep_within: %w", err)
		}
	}
	return nil
}

func BoolOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

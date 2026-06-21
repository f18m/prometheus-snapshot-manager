package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/f18m/prometheus-snapshot-manager/internal/utils"
	"gopkg.in/yaml.v3"
)

// Enum types for configuration values
type CompressionFormat string

const (
	CompressionFormatTarGz  CompressionFormat = "tar.gz"
	CompressionFormatTarBz2 CompressionFormat = "tar.bz2"
	CompressionFormatTarXz  CompressionFormat = "tar.xz"
	CompressionFormatTar    CompressionFormat = "tar"
)

type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

type LogFormat string

const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

type TargetType string

const (
	TargetTypeLocal TargetType = "local"
	TargetTypeSFTP  TargetType = "sftp"
	TargetTypeS3    TargetType = "s3"
)

// The Config struct represents the configuration for the entire application.
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
	URL                    string
	Timeout                time.Duration
	SnapshotDir            string
	SnapshotArchiveTempDir string
	TLSSkipVerify          bool
	BasicAuth              PromBasicAuth
}

// UnmarshalYAML parses YAML into PrometheusConfig, converting timeout string to time.Duration.
func (p *PrometheusConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		URL                    string        `yaml:"url"`
		Timeout                string        `yaml:"timeout"`
		SnapshotDir            string        `yaml:"snapshot_dir"`
		SnapshotArchiveTempDir string        `yaml:"snapshot_archive_temp_dir"`
		TLSSkipVerify          bool          `yaml:"tls_skip_verify"`
		BasicAuth              PromBasicAuth `yaml:"basic_auth"`
	}
	r := &raw{}
	if err := value.Decode(r); err != nil {
		return err
	}
	p.URL = r.URL
	p.SnapshotDir = r.SnapshotDir
	p.SnapshotArchiveTempDir = r.SnapshotArchiveTempDir
	p.TLSSkipVerify = r.TLSSkipVerify
	p.BasicAuth = r.BasicAuth

	if r.Timeout != "" {
		d, err := utils.ParseDuration(r.Timeout)
		if err != nil {
			return fmt.Errorf("invalid prometheus.timeout: %w", err)
		}
		p.Timeout = d
	}
	return nil
}

type PromBasicAuth struct {
	Username string
	Password string
}

type ScheduleConfig struct {
	Cron         string
	Interval     *time.Duration
	Timezone     *time.Location
	RunOnStartup bool
}

// UnmarshalYAML parses YAML into ScheduleConfig, converting timezone string to *time.Location
// and interval string to *time.Duration.
func (s *ScheduleConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		Cron         string `yaml:"cron"`
		Interval     string `yaml:"interval"`
		Timezone     string `yaml:"timezone"`
		RunOnStartup bool   `yaml:"run_on_startup"`
	}
	r := &raw{}
	if err := value.Decode(r); err != nil {
		return err
	}
	s.Cron = r.Cron
	s.RunOnStartup = r.RunOnStartup

	// Parse interval if provided
	if r.Interval != "" {
		d, err := utils.ParseDuration(r.Interval)
		if err != nil {
			return fmt.Errorf("invalid schedule.interval: %w", err)
		}
		s.Interval = &d
	}

	// Parse timezone if provided, default to UTC
	tz := r.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("invalid schedule.timezone: %w", err)
	}
	s.Timezone = loc

	return nil
}

type CompressionConfig struct {
	Format CompressionFormat
	Level  int
}

// UnmarshalYAML parses YAML into CompressionConfig, converting format string to CompressionFormat.
func (c *CompressionConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		Format string `yaml:"format"`
		Level  int    `yaml:"level"`
	}
	r := &raw{}
	if err := value.Decode(r); err != nil {
		return err
	}
	c.Level = r.Level
	c.Format = CompressionFormat(r.Format)
	return nil
}

type RetentionConfig struct {
	KeepWithin                 *time.Duration
	KeepMinimum                int
	KeepMaximum                int
	KeepDaily                  int
	KeepWeekly                 int
	KeepMonthly                int
	CleanupPrometheusSnapshots bool
}

// UnmarshalYAML parses YAML into RetentionConfig, converting keep_within string to *time.Duration.
func (r *RetentionConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		KeepWithin                 string `yaml:"keep_within"`
		KeepMinimum                int    `yaml:"keep_minimum"`
		KeepMaximum                int    `yaml:"keep_maximum"`
		KeepDaily                  int    `yaml:"keep_daily"`
		KeepWeekly                 int    `yaml:"keep_weekly"`
		KeepMonthly                int    `yaml:"keep_monthly"`
		CleanupPrometheusSnapshots bool   `yaml:"cleanup_prometheus_snapshots"`
	}
	rawData := &raw{}
	if err := value.Decode(rawData); err != nil {
		return err
	}
	r.KeepMinimum = rawData.KeepMinimum
	r.KeepMaximum = rawData.KeepMaximum
	r.KeepDaily = rawData.KeepDaily
	r.KeepWeekly = rawData.KeepWeekly
	r.KeepMonthly = rawData.KeepMonthly
	r.CleanupPrometheusSnapshots = rawData.CleanupPrometheusSnapshots

	if rawData.KeepWithin != "" {
		d, err := utils.ParseDuration(rawData.KeepWithin)
		if err != nil {
			return fmt.Errorf("invalid retention.keep_within: %w", err)
		}
		r.KeepWithin = &d
	}
	return nil
}

type TargetConfig struct {
	Name  string
	Type  TargetType
	Local LocalConfig
	SFTP  SFTPConfig
	S3    S3Config
}

// UnmarshalYAML parses YAML into TargetConfig, converting type string to TargetType.
func (t *TargetConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		Name  string      `yaml:"name"`
		Type  string      `yaml:"type"`
		Local LocalConfig `yaml:"local"`
		SFTP  SFTPConfig  `yaml:"sftp"`
		S3    S3Config    `yaml:"s3"`
	}
	r := &raw{}
	if err := value.Decode(r); err != nil {
		return err
	}
	t.Name = r.Name
	t.Type = TargetType(r.Type)
	t.Local = r.Local
	t.SFTP = r.SFTP
	t.S3 = r.S3
	return nil
}

type LocalConfig struct {
	Path string
}

type SFTPConfig struct {
	Host           string
	Port           int
	User           string
	Password       string
	KeyFile        string
	KeyData        string
	RemotePath     string
	HostKeyCheck   bool
	KnownHostsFile string
}

type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	StorageClass    string
	ForcePathStyle  bool
}

type NotificationConfig struct {
	Apprise AppriseConfig
}

type AppriseConfig struct {
	Enabled       bool
	ConfigFile    string
	URLs          []string
	OnSuccess     *bool
	OnFailure     *bool
	TitleTemplate string
	BodyTemplate  string
}

type LoggingConfig struct {
	Level  LogLevel
	Format LogFormat
	Output string
}

// UnmarshalYAML parses YAML into LoggingConfig, converting level and format strings to enums.
func (l *LoggingConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
		Output string `yaml:"output"`
	}
	r := &raw{}
	if err := value.Decode(r); err != nil {
		return err
	}
	l.Level = LogLevel(r.Level)
	l.Format = LogFormat(r.Format)
	l.Output = r.Output
	return nil
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
	if c.Prometheus.Timeout == 0 {
		c.Prometheus.Timeout = 30 * time.Second
	}
	if c.Prometheus.SnapshotDir == "" {
		c.Prometheus.SnapshotDir = "/prometheus/snapshots"
	}
	if c.Prometheus.SnapshotArchiveTempDir == "" {
		c.Prometheus.SnapshotArchiveTempDir = "/prometheus/snapshots-mgr-temp"
	}
	if c.Compression.Format == "" {
		c.Compression.Format = CompressionFormatTarGz
	}
	if c.Compression.Level == 0 {
		c.Compression.Level = 6
	}
	if c.Logging.Level == "" {
		c.Logging.Level = LogLevelInfo
	}
	if c.Logging.Format == "" {
		c.Logging.Format = LogFormatJSON
	}
	if c.Logging.Output == "" {
		c.Logging.Output = "stdout"
	}
	if c.Schedule.Timezone == nil {
		c.Schedule.Timezone = time.UTC
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
	if c.Prometheus.SnapshotArchiveTempDir == "" {
		return errors.New("prometheus.snapshot_archive_temp_dir is required")
	}
	// Timeout validation already done in UnmarshalYAML

	if c.Schedule.Cron == "" && c.Schedule.Interval == nil {
		return errors.New("either schedule.cron or schedule.interval is required")
	}
	// Timezone and Interval validation already done in UnmarshalYAML

	// Validate compression format
	switch c.Compression.Format {
	case CompressionFormatTarGz, CompressionFormatTarBz2, CompressionFormatTarXz, CompressionFormatTar:
		// valid
	default:
		return fmt.Errorf("compression.format unsupported: %s", c.Compression.Format)
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
		case TargetTypeLocal:
			if t.Local.Path == "" {
				return fmt.Errorf("targets[%d].local.path is required", i)
			}
		case TargetTypeSFTP:
			if t.SFTP.Host == "" || t.SFTP.User == "" || t.SFTP.RemotePath == "" {
				return fmt.Errorf("targets[%d].sftp host/user/remote_path are required", i)
			}
			if t.SFTP.Port == 0 {
				c.Targets[i].SFTP.Port = 22
			}
		case TargetTypeS3:
			if t.S3.Region == "" || t.S3.Bucket == "" {
				return fmt.Errorf("targets[%d].s3 region/bucket are required", i)
			}
		default:
			return fmt.Errorf("targets[%d].type unsupported: %s", i, t.Type)
		}
	}
	// KeepWithin validation already done in UnmarshalYAML

	// Validate logging level
	switch c.Logging.Level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		// valid
	default:
		return fmt.Errorf("logging.level unsupported: %s", c.Logging.Level)
	}

	// Validate logging format
	switch c.Logging.Format {
	case LogFormatJSON, LogFormatText:
		// valid
	default:
		return fmt.Errorf("logging.format unsupported: %s", c.Logging.Format)
	}

	return nil
}

func BoolOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

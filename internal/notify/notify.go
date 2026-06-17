package notify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"text/template"
	"time"

	"github.com/f18m/prometheus-snapshot-manager/internal/config"
)

type Payload struct {
	Status       string
	SnapshotName string
	SizeHuman    string
	Duration     time.Duration
	Targets      string
	Error        string
}

type Notifier struct {
	cfg config.AppriseConfig
}

func New(cfg config.AppriseConfig) *Notifier {
	return &Notifier{cfg: cfg}
}

func (n *Notifier) Send(ctx context.Context, payload Payload) error {
	if !n.cfg.Enabled {
		return nil
	}
	if payload.Status == "success" && !config.BoolOrDefault(n.cfg.OnSuccess, true) {
		return nil
	}
	if payload.Status == "failure" && !config.BoolOrDefault(n.cfg.OnFailure, true) {
		return nil
	}
	title, err := render(n.cfg.TitleTemplate, payload)
	if err != nil {
		return err
	}
	body, err := render(n.cfg.BodyTemplate, payload)
	if err != nil {
		return err
	}
	args := []string{"-t", title, "-b", body}
	if n.cfg.ConfigFile != "" {
		args = append(args, "-c", n.cfg.ConfigFile)
	}
	args = append(args, n.cfg.URLs...)
	cmd := exec.CommandContext(ctx, "apprise", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apprise failed: %w: %s", err, string(out))
	}
	return nil
}

func render(tpl string, data Payload) (string, error) {
	t, err := template.New("tpl").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

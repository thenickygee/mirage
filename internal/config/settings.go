package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type InstanceDisplay struct {
	ShowCtx   bool `yaml:"show_ctx"`
	ShowModel bool `yaml:"show_model"`
	ShowMsgs  bool `yaml:"show_msgs"`
	ShowInOut bool `yaml:"show_in_out"`
}

func DefaultInstanceDisplay() InstanceDisplay {
	return InstanceDisplay{ShowCtx: true, ShowModel: true, ShowMsgs: true, ShowInOut: true}
}

type Settings struct {
	AccentColor     string          `yaml:"accent_color"`
	ListOnlyMode    bool            `yaml:"list_only_mode"`
	InstanceDisplay InstanceDisplay `yaml:"instance_display"`
}

func DefaultSettings() Settings {
	return Settings{AccentColor: "lime", ListOnlyMode: true, InstanceDisplay: DefaultInstanceDisplay()}
}

func settingsPath() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Config, "agent-view.yaml"), nil
}

func LoadSettings() Settings {
	p, err := settingsPath()
	if err != nil {
		return DefaultSettings()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return DefaultSettings()
	}
	// Start with defaults so new fields are true unless explicitly set false.
	s := DefaultSettings()
	if err := yaml.Unmarshal(data, &s); err != nil {
		return DefaultSettings()
	}
	if s.AccentColor == "" {
		s.AccentColor = "lime"
	}
	return s
}

func SaveSettings(s Settings) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

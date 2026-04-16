package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Lima    LimaConfig    `yaml:"lima"`
	Belayer BelayerConfig `yaml:"belayer"`
}

type LimaConfig struct {
	VMName string `yaml:"vm_name"`
}

type BelayerConfig struct {
	// SocketPath is the belayer daemon socket inside the VM.
	SocketPath string `yaml:"socket_path"`
	// WorkspaceMount is the in-VM directory where git URLs are cloned.
	WorkspaceMount string `yaml:"workspace_mount"`
	// Binary is the belayer executable path inside the VM.
	Binary string `yaml:"binary"`
}

func defaults() *Config {
	return &Config{
		Lima: LimaConfig{VMName: "devbox"},
		Belayer: BelayerConfig{
			SocketPath: "/run/user/1000/belayer/belayer.sock",
			// Absolute in-VM path. `~` is intentionally avoided — the value is
			// shellQuoted into commands run via `bash -lc`, where single quotes
			// suppress tilde expansion.
			WorkspaceMount: "/var/tmp/crag-workspaces",
			Binary:         "belayer",
		},
	}
}

// Path returns the resolved config file location (~/.crag/config.yaml).
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".crag", "config.yaml"), nil
}

// Load reads the config file, writing defaults if it does not yet exist.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := defaults()
		if err := Save(cfg); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to ~/.crag/config.yaml, creating the directory if needed.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

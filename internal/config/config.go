package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Portal  string `json:"portal"`
	Browser string `json:"browser"`
}

func dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gpclient-gui"), nil
}

func Load() (*Config, error) {
	d, err := dir()
	if err != nil {
		return defaultConfig(), nil
	}
	data, err := os.ReadFile(filepath.Join(d, "config.json"))
	if err != nil {
		return defaultConfig(), nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), nil
	}
	return &cfg, nil
}

func Save(cfg *Config) error {
	d, err := dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "config.json"), data, 0o600)
}

func defaultConfig() *Config {
	return &Config{Browser: "default"}
}

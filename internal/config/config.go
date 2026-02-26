// Package config loads and validates context-bridge configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the root configuration structure.
type Config struct {
	Cmux      CmuxConfig      `toml:"cmux"`
	Bridge    BridgeConfig    `toml:"bridge"`
	Anthropic AnthropicConfig `toml:"anthropic"`
}

type CmuxConfig struct {
	SocketPath string `toml:"socket_path"`
}

type BridgeConfig struct {
	PollIntervalSeconds int    `toml:"poll_interval_seconds"`
	MaxScrollbackLines  int    `toml:"max_scrollback_lines"`
	DBPath              string `toml:"db_path"`
	LogLevel            string `toml:"log_level"`
}

type AnthropicConfig struct {
	APIKey    string `toml:"api_key"`
	Model     string `toml:"model"`
	MaxTokens int    `toml:"max_tokens"`
}

// Load reads the config file at path. Missing file is not an error — defaults are applied.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path == "" {
		path = defaultConfigPath()
	}

	path = expandHome(path)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil // use defaults
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

// PollInterval returns the poll interval as a time.Duration.
func (c *Config) PollInterval() time.Duration {
	return time.Duration(c.Bridge.PollIntervalSeconds) * time.Second
}

// SocketPath returns the resolved cmux socket path.
func (c *Config) SocketPath() string {
	if c.Cmux.SocketPath != "" {
		return c.Cmux.SocketPath
	}
	return os.Getenv("CMUX_SOCKET_PATH")
}

func defaults() *Config {
	return &Config{
		Bridge: BridgeConfig{
			PollIntervalSeconds: 5,
			MaxScrollbackLines:  300,
			DBPath:              "~/.context-bridge/sessions.db",
			LogLevel:            "info",
		},
		Anthropic: AnthropicConfig{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 2048,
		},
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		cfg.Anthropic.APIKey = v
	}
	if v := os.Getenv("CMUX_SOCKET_PATH"); v != "" && cfg.Cmux.SocketPath == "" {
		cfg.Cmux.SocketPath = v
	}
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "context-bridge", "config.toml")
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

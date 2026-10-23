// Package config loads settings from defaults, a TOML file, env, and flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"github.com/elee1766/jcode-web/internal/jcode"
)

type Config struct {
	Listen       string        `koanf:"listen"`
	DataDir      string        `koanf:"data_dir"`
	MainSocket   string        `koanf:"main_socket"`
	DebugSocket  string        `koanf:"debug_socket"`
	PollInterval time.Duration `koanf:"poll_interval"`
	Retention    time.Duration `koanf:"retention"`
	AttachLinger time.Duration `koanf:"attach_linger"`
	PushSubject  string        `koanf:"push_subject"`
	LogLevel     string        `koanf:"log_level"`
	LogFormat    string        `koanf:"log_format"`
	Summary      Summary       `koanf:"summary"`
}

// Summary configures an OpenAI-compatible model for AI summaries.
type Summary struct {
	Enabled         bool          `koanf:"enabled"`
	BaseURL         string        `koanf:"base_url"`
	APIKey          string        `koanf:"api_key"`
	Model           string        `koanf:"model"`
	ReasoningEffort string        `koanf:"reasoning_effort"`
	MaxTokens       int           `koanf:"max_tokens"`
	Timeout         time.Duration `koanf:"timeout"`
}

func Defaults() Config {
	mainSock, debugSock := jcode.DefaultSockets()
	data := os.Getenv("XDG_STATE_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "state")
	}
	return Config{
		Listen:       "127.0.0.1:7650",
		DataDir:      filepath.Join(data, "jcode-web"),
		MainSocket:   mainSock,
		DebugSocket:  debugSock,
		PollInterval: 3 * time.Second,
		Retention:    30 * 24 * time.Hour,
		AttachLinger: 2 * time.Minute,
		PushSubject:  "mailto:jcode-web@localhost",
		LogLevel:     "info",
		LogFormat:    "text",
		Summary: Summary{
			ReasoningEffort: "low",
			MaxTokens:       400,
			Timeout:         60 * time.Second,
		},
	}
}

// DefaultPath is ~/.config/jcode-web/config.toml.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "jcode-web", "config.toml")
}

// Load merges defaults, the file at path (optional when missing and
// implicit), JCODE_WEB_* env vars, then overrides.
func Load(path string, explicit bool, overrides map[string]any) (Config, error) {
	k := koanf.New(".")
	if err := k.Load(structs.Provider(Defaults(), "koanf"), nil); err != nil {
		return Config{}, err
	}
	if path != "" {
		if _, err := os.Stat(path); err == nil || explicit {
			if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
				return Config{}, fmt.Errorf("load %s: %w", path, err)
			}
		}
	}
	err := k.Load(env.Provider(".", env.Opt{
		Prefix: "JCODE_WEB_",
		// JCODE_WEB_SUMMARY__BASE_URL -> summary.base_url
		TransformFunc: func(key, value string) (string, any) {
			k := strings.ToLower(strings.TrimPrefix(key, "JCODE_WEB_"))
			return strings.ReplaceAll(k, "__", "."), value
		},
	}), nil)
	if err != nil {
		return Config{}, err
	}
	for key, v := range overrides {
		if err := k.Set(key, v); err != nil {
			return Config{}, err
		}
	}
	var c Config
	if err := k.UnmarshalWithConf("", &c, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return Config{}, err
	}
	return c, nil
}

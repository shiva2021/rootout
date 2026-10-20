// Package config loads and saves purge's TOML settings file
// (~/.config/purge/config.toml). It never holds secrets; API keys live in the
// Keychain (D-006).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/shiva2021/purge/internal/fsx"
)

// Config is the whole settings file.
type Config struct {
	LLM  LLM  `toml:"llm"`
	Scan Scan `toml:"scan"`
}

// LLM selects and configures the model provider.
type LLM struct {
	Provider  string `toml:"provider"` // "" | "anthropic" | "openai" | "ollama"
	Anthropic Hosted `toml:"anthropic"`
	OpenAI    Hosted `toml:"openai"`
	Ollama    Ollama `toml:"ollama"`
}

// Hosted configures a hosted provider.
type Hosted struct {
	Model string `toml:"model"`
}

// Ollama configures a local Ollama server.
type Ollama struct {
	BaseURL string `toml:"base_url"`
	Model   string `toml:"model"`
}

// Scan holds scan defaults.
type Scan struct {
	MinScore int  `toml:"min_score"`
	Online   bool `toml:"online"`
}

// Default returns the built-in settings.
func Default() Config {
	return Config{
		LLM: LLM{
			Anthropic: Hosted{Model: "claude-sonnet-5-5"},
			OpenAI:    Hosted{Model: "gpt-5.2"},
			Ollama:    Ollama{BaseURL: "http://localhost:11434"},
		},
		Scan: Scan{MinScore: 30},
	}
}

// Path returns $PURGE_CONFIG if set, else ~/.config/purge/config.toml.
func Path(env fsx.Env, getenv func(string) string) string {
	if getenv != nil {
		if p := getenv("PURGE_CONFIG"); p != "" {
			return p
		}
	}
	return env.User(".config", "purge", "config.toml")
}

// Load reads path. A missing file yields Default(). Unknown keys are an error.
func Load(path string) (Config, error) {
	c := Default()
	md, err := toml.DecodeFile(path, &c)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		names := make([]string, len(und))
		for i, k := range und {
			names[i] = k.String()
		}
		return Config{}, fmt.Errorf("config: %s: unknown keys: %s", path, strings.Join(names, ", "))
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return c, nil
}

// Save writes c to path atomically (0600 file, 0700 directory).
func Save(path string, c Config) error {
	if err := c.validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: create temp in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: rename to %s: %w", path, err)
	}
	return nil
}

// field binds a dotted key to its getter and setter.
type field struct {
	get func(c *Config) string
	set func(c *Config, v string) error
}

func str(p func(c *Config) *string, check func(string) error) field {
	return field{
		get: func(c *Config) string { return *p(c) },
		set: func(c *Config, v string) error {
			if check != nil {
				if err := check(v); err != nil {
					return err
				}
			}
			*p(c) = v
			return nil
		},
	}
}

var fields = map[string]field{
	"llm.provider":        str(func(c *Config) *string { return &c.LLM.Provider }, checkProvider),
	"llm.anthropic.model": str(func(c *Config) *string { return &c.LLM.Anthropic.Model }, nil),
	"llm.openai.model":    str(func(c *Config) *string { return &c.LLM.OpenAI.Model }, nil),
	"llm.ollama.base_url": str(func(c *Config) *string { return &c.LLM.Ollama.BaseURL }, checkBaseURL),
	"llm.ollama.model":    str(func(c *Config) *string { return &c.LLM.Ollama.Model }, nil),
	"scan.min_score": {
		get: func(c *Config) string { return strconv.Itoa(c.Scan.MinScore) },
		set: func(c *Config, v string) error {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("scan.min_score: %q is not an integer", v)
			}
			if err := checkMinScore(n); err != nil {
				return err
			}
			c.Scan.MinScore = n
			return nil
		},
	},
	"scan.online": {
		get: func(c *Config) string { return strconv.FormatBool(c.Scan.Online) },
		set: func(c *Config, v string) error {
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("scan.online: %q is not a boolean", v)
			}
			c.Scan.Online = b
			return nil
		},
	},
}

// Keys returns the valid dotted keys, sorted.
func Keys() []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func unknownKey(key string) error {
	return fmt.Errorf("config: unknown key %q (valid keys: %s)", key, strings.Join(Keys(), ", "))
}

// Set changes one dotted key from a string value.
func (c *Config) Set(key, value string) error {
	f, ok := fields[key]
	if !ok {
		return unknownKey(key)
	}
	if err := f.set(c, value); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// Get returns one dotted key's value as a string.
func (c Config) Get(key string) (string, error) {
	f, ok := fields[key]
	if !ok {
		return "", unknownKey(key)
	}
	return f.get(&c), nil
}

func (c Config) validate() error {
	if err := checkProvider(c.LLM.Provider); err != nil {
		return err
	}
	if err := checkBaseURL(c.LLM.Ollama.BaseURL); err != nil {
		return err
	}
	return checkMinScore(c.Scan.MinScore)
}

func checkProvider(v string) error {
	switch v {
	case "", "anthropic", "openai", "ollama":
		return nil
	}
	return fmt.Errorf("llm.provider: %q is not one of anthropic, openai, ollama (or empty)", v)
}

func checkBaseURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("llm.ollama.base_url: %q is not an http(s) URL", v)
	}
	return nil
}

func checkMinScore(n int) error {
	if n < 0 || n > 100 {
		return fmt.Errorf("scan.min_score: %d is outside 0..100", n)
	}
	return nil
}

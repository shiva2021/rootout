package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/shiva2021/rootout/internal/fsx"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("got %+v, want defaults", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "rootout", "config.toml")
	want := Default()
	want.LLM.Provider = "ollama"
	want.LLM.Ollama.Model = "llama3.3"
	want.Scan.Online = true
	want.Scan.MinScore = 55
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if m := fi.Mode().Perm(); m != 0o600 {
		t.Errorf("file mode %o, want 600", m)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if m := di.Mode().Perm(); m != 0o700 {
		t.Errorf("dir mode %o, want 700", m)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("leftover temp files: %v", entries)
	}
}

func TestLoadPartialKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[llm]\nprovider = \"openai\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.LLM.Provider = "openai"
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %+v, want %+v", c, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"unknown key", "[llm]\napi_key = \"x\"\n[scan]\nfoo = 1\n", "llm.api_key"},
		{"unknown table", "[bogus]\na = 1\n", "bogus"},
		{"bad provider", "[llm]\nprovider = \"gemini\"\n", "llm.provider"},
		{"bad score", "[scan]\nmin_score = 101\n", "scan.min_score"},
		{"bad url", "[llm.ollama]\nbase_url = \"ftp://x\"\n", "base_url"},
		{"syntax", "[llm\n", "config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want mention of %q", err, tt.want)
			}
		})
	}
}

func TestMarshalNeverContainsKeys(t *testing.T) {
	var buf strings.Builder
	c := Default()
	c.LLM.Provider = "anthropic"
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if strings.Contains(strings.ToLower(name), "key") {
			t.Errorf("marshalled config has a key-like field: %q", line)
		}
	}
	for _, k := range Keys() {
		if strings.Contains(k, "key") || strings.Contains(k, "secret") || strings.Contains(k, "token") {
			t.Errorf("settable key %q looks like a secret", k)
		}
	}
}

func TestSetGet(t *testing.T) {
	tests := []struct {
		key, value, want string
		wantErr          bool
	}{
		{"llm.provider", "anthropic", "anthropic", false},
		{"llm.provider", "openai", "openai", false},
		{"llm.provider", "ollama", "ollama", false},
		{"llm.provider", "", "", false},
		{"llm.provider", "gemini", "", true},
		{"llm.anthropic.model", "claude-opus-5-5", "claude-opus-5-5", false},
		{"llm.openai.model", "gpt-5.2-mini", "gpt-5.2-mini", false},
		{"llm.ollama.base_url", "https://box.local:11434", "https://box.local:11434", false},
		{"llm.ollama.base_url", "localhost:11434", "", true},
		{"llm.ollama.base_url", "ftp://x", "", true},
		{"llm.ollama.model", "qwen3", "qwen3", false},
		{"scan.min_score", "0", "0", false},
		{"scan.min_score", "100", "100", false},
		{"scan.min_score", "-1", "", true},
		{"scan.min_score", "101", "", true},
		{"scan.min_score", "abc", "", true},
		{"scan.online", "true", "true", false},
		{"scan.online", "false", "false", false},
		{"scan.online", "maybe", "", true},
		{"llm.api_key", "x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			c := Default()
			before := c
			err := c.Set(tt.key, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !reflect.DeepEqual(c, before) {
					t.Error("failed Set modified config")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Get(tt.key)
			if err != nil || got != tt.want {
				t.Errorf("Get = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestUnknownKeyListsValidKeys(t *testing.T) {
	c := Default()
	err := c.Set("nope", "1")
	if err == nil || !strings.Contains(err.Error(), "scan.min_score") {
		t.Errorf("Set error = %v", err)
	}
	if _, err := c.Get("nope"); err == nil || !strings.Contains(err.Error(), "llm.provider") {
		t.Errorf("Get error = %v", err)
	}
}

func TestKeysSortedAndGettable(t *testing.T) {
	keys := Keys()
	want := []string{"llm.anthropic.model", "llm.ollama.base_url", "llm.ollama.model", "llm.openai.model", "llm.provider", "scan.min_score", "scan.online"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("Keys = %v", keys)
	}
	c := Default()
	for _, k := range keys {
		if _, err := c.Get(k); err != nil {
			t.Errorf("Get(%s): %v", k, err)
		}
	}
}

func TestPath(t *testing.T) {
	env := fsx.Env{Home: "/fake/home", Root: "/fake/root"}
	none := func(string) string { return "" }
	if got := Path(env, none); got != "/fake/home/.config/rootout/config.toml" {
		t.Errorf("default Path = %q", got)
	}
	override := func(k string) string {
		if k == "ROOTOUT_CONFIG" {
			return "/elsewhere/c.toml"
		}
		return ""
	}
	if got := Path(env, override); got != "/elsewhere/c.toml" {
		t.Errorf("override Path = %q", got)
	}
}

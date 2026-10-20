package keychain

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestStore(t *testing.T) {
	keyring.MockInit()
	s := New()

	if _, err := s.Get("anthropic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: got %v, want ErrNotFound", err)
	}
	if err := s.Set("anthropic", "sk-ant-123"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("anthropic")
	if err != nil || got != "sk-ant-123" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := s.Delete("anthropic"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("anthropic"); err != nil {
		t.Fatalf("Delete absent: %v", err)
	}
	if _, err := s.Get("anthropic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
}

func TestValidation(t *testing.T) {
	keyring.MockInit()
	s := New()
	for _, p := range []string{"", "Anthropic", "a b", "../x", "abcdefghijklmnopqrstuvwxyz0123456"} {
		t.Run("provider "+p, func(t *testing.T) {
			if err := s.Set(p, "k"); err == nil {
				t.Error("Set accepted invalid provider")
			}
			if _, err := s.Get(p); err == nil || errors.Is(err, ErrNotFound) {
				t.Errorf("Get: %v", err)
			}
			if err := s.Delete(p); err == nil {
				t.Error("Delete accepted invalid provider")
			}
		})
	}
	for _, k := range []string{"", "a\nb", "a\rb"} {
		if err := s.Set("openai", k); err == nil {
			t.Errorf("Set accepted key %q", k)
		}
	}
}

func TestResolve(t *testing.T) {
	keyring.MockInit()
	s := New()
	if err := s.Set("anthropic", "from-keychain"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("ollama", "ollama-key"); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv(map[string]string{"OPENAI_API_KEY": "from-env", "OLLAMA_API_KEY": "ignored"})

	tests := []struct {
		provider, env, key, source string
		wantErr                    error
	}{
		{"anthropic", "", "from-keychain", "keychain", nil},
		{"openai", "", "from-env", "env", nil},
		{"ollama", "", "ollama-key", "keychain", nil},
		{"other", "", "", "", ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			key, src, err := Resolve(s, tt.provider, env)
			if !errors.Is(err, tt.wantErr) || key != tt.key || src != tt.source {
				t.Errorf("Resolve = %q, %q, %v; want %q, %q, %v", key, src, err, tt.key, tt.source, tt.wantErr)
			}
		})
	}

	// env overrides the keychain
	key, src, err := Resolve(s, "anthropic", fakeEnv(map[string]string{"ANTHROPIC_API_KEY": "env-ant"}))
	if err != nil || key != "env-ant" || src != "env" {
		t.Errorf("override: %q %q %v", key, src, err)
	}
}

func TestMask(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"short", "•••••"},
		{"sk-ant-api03-abcdefghijWXYZ", "sk-ant-…WXYZ"},
		{"0123456789abcdef", "0123456…cdef"},
	}
	for _, tt := range tests {
		if got := Mask(tt.in); got != tt.want {
			t.Errorf("Mask(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

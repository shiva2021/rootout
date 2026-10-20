// Package keychain stores LLM provider API keys in the macOS login Keychain
// (D-006, D-012). Environment variables override stored keys.
package keychain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
)

// service is the Keychain service name; the account is the provider name.
const service = "rootout"

// ErrNotFound is returned when no key is stored for a provider.
var ErrNotFound = errors.New("keychain: no key stored")

var providerRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// envVars maps providers to the environment variable that overrides the Keychain.
var envVars = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
}

// Store reads and writes API keys.
type Store interface {
	Get(provider string) (string, error) // ErrNotFound when absent
	Set(provider, key string) error
	Delete(provider string) error // nil when already absent
}

// New returns a Store backed by go-keyring.
func New() Store { return keyringStore{} }

type keyringStore struct{}

func validProvider(provider string) error {
	if !providerRe.MatchString(provider) {
		return fmt.Errorf("keychain: invalid provider name %q", provider)
	}
	return nil
}

func (keyringStore) Get(provider string) (string, error) {
	if err := validProvider(provider); err != nil {
		return "", err
	}
	key, err := keyring.Get(service, provider)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keychain: get %s: %w", provider, err)
	}
	return key, nil
}

func (keyringStore) Set(provider, key string) error {
	if err := validProvider(provider); err != nil {
		return err
	}
	if key == "" {
		return errors.New("keychain: empty key")
	}
	if strings.ContainsAny(key, "\r\n") {
		return errors.New("keychain: key contains a newline")
	}
	if err := keyring.Set(service, provider, key); err != nil {
		return fmt.Errorf("keychain: set %s: %w", provider, err)
	}
	return nil
}

func (keyringStore) Delete(provider string) error {
	if err := validProvider(provider); err != nil {
		return err
	}
	err := keyring.Delete(service, provider)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keychain: delete %s: %w", provider, err)
	}
	return nil
}

// Resolve returns the key for provider: environment variable first
// (ANTHROPIC_API_KEY for "anthropic", OPENAI_API_KEY for "openai"; providers
// without a variable skip this), then the Store. source is "env" or
// "keychain". Returns ErrNotFound when neither has a key.
func Resolve(s Store, provider string, getenv func(string) string) (key, source string, err error) {
	if err := validProvider(provider); err != nil {
		return "", "", err
	}
	if name, ok := envVars[provider]; ok && getenv != nil {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v, "env", nil
		}
	}
	key, err = s.Get(provider)
	if err != nil {
		return "", "", err
	}
	return key, "keychain", nil
}

// Mask renders a key for display: the first 7 and last 4 characters with "…"
// between. Keys too short to hide anything are fully masked.
func Mask(key string) string {
	r := []rune(key)
	if len(r) < 16 {
		return strings.Repeat("•", len(r))
	}
	return string(r[:7]) + "…" + string(r[len(r)-4:])
}

package brew

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

func fake(fail bool, calls *[]call) *Brew {
	return &Brew{
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			*calls = append(*calls, call{name, args})
			if fail {
				return []byte("Error: Cask 'nope' is not installed."), errors.New("exit status 1")
			}
			return []byte("==> Uninstalling Cask ghostty"), nil
		},
		LookPath: func(string) (string, error) { return "/opt/homebrew/bin/brew", nil },
	}
}

func TestZap(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		fail    bool
		wantErr string
		wantRun bool
	}{
		{"ok", "ghostty", false, "", true},
		{"versioned token", "openjdk@17", false, "", true},
		{"brew failure surfaces output", "nope", true, "not installed", true},
		{"hostile token rejected before exec", "x; rm -rf /", false, "invalid cask token", false},
		{"uppercase rejected", "Ghostty", false, "invalid cask token", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			b := fake(tc.fail, &calls)
			err := b.Zap(context.Background(), tc.token)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			if tc.wantRun != (len(calls) == 1) {
				t.Fatalf("runner called %d times, wantRun=%v", len(calls), tc.wantRun)
			}
			if tc.wantRun {
				want := []string{"uninstall", "--cask", "--zap", tc.token}
				if calls[0].name != "brew" || strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
					t.Fatalf("argv = %s %v, want brew %v", calls[0].name, calls[0].args, want)
				}
			}
		})
	}
}

func TestZapWithoutBrew(t *testing.T) {
	b := &Brew{
		Run:      func(context.Context, string, ...string) ([]byte, error) { t.Fatal("must not run"); return nil, nil },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	if err := b.Zap(context.Background(), "ghostty"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
	if b.Available() {
		t.Fatal("Available() = true without brew")
	}
}

package update

import (
	"strings"
	"testing"
)

func TestInstalled(t *testing.T) {
	tests := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"v0.7.0", [3]int{0, 7, 0}, true},
		{"v0.7.0-3-gabc1234-dirty", [3]int{0, 7, 0}, true},
		{"v1.12.3", [3]int{1, 12, 3}, true},
		{"0.7.0", [3]int{0, 7, 0}, true},
		{"dev", [3]int{}, false},
		{"", [3]int{}, false},
		{"v1.2", [3]int{}, false},
		{"vX.1.2", [3]int{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := Installed(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Installed(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestNewer(t *testing.T) {
	tests := []struct {
		name              string
		latest, installed string
		want              bool
	}{
		{"minor bump", "v0.8.0", "v0.7.0", true},
		{"patch bump", "v0.7.1", "v0.7.0", true},
		{"major bump", "v1.0.0", "v0.9.9", true},
		{"numeric not lexical", "v0.10.0", "v0.9.0", true},
		{"equal", "v0.7.0", "v0.7.0", false},
		{"local build past the tag is not older", "v0.7.0", "v0.7.0-3-gabc1234-dirty", false},
		{"older latest", "v0.6.0", "v0.7.0", false},
		{"dev build", "v0.8.0", "dev", false},
		{"garbage latest", "latest", "v0.7.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Newer(tt.latest, tt.installed); got != tt.want {
				t.Errorf("Newer(%q, %q) = %v, want %v", tt.latest, tt.installed, got, tt.want)
			}
		})
	}
}

func TestCanonical(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v0.8.0", "v0.8.0"},
		{"v0.8.0-3-gabc1234-dirty", "v0.8.0"},
		{"v9.9.9-日本語\x1b[31m", "v9.9.9"},
		{"0.8.0", "v0.8.0"},
		{"dev", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Canonical(tt.in); got != tt.want {
				t.Errorf("Canonical(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReleasesPageSharesHostWithDefaultURL(t *testing.T) {
	if !strings.HasPrefix(ReleasesPage, "https://github.com/shiva2021/rootout/") || !strings.Contains(DefaultURL, "/repos/shiva2021/rootout/") {
		t.Errorf("ReleasesPage %q and DefaultURL %q name different repos", ReleasesPage, DefaultURL)
	}
}

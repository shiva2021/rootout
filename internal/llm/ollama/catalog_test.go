package ollama

import (
	"strings"
	"testing"
)

func TestFit(t *testing.T) {
	tests := []struct {
		size, ram int64
		want      string
	}{
		{600, 1200, FitComfortable}, // resident 720 = 60 %
		{601, 1200, FitTight},
		{800, 1200, FitTight}, // resident 960 = 80 %
		{801, 1200, FitTooLarge},
		{0, 8 << 30, FitComfortable},
		{4 * gb, 8 << 30, FitComfortable},
		{20 * gb, 32 << 30, FitTight},
		{19 * gb, 16 << 30, FitTooLarge},
	}
	for _, tt := range tests {
		if got := Fit(tt.size, tt.ram); got != tt.want {
			t.Errorf("Fit(%d, %d) = %q, want %q", tt.size, tt.ram, got, tt.want)
		}
	}
}

type rec struct{ name, fit string }

func TestRecommend(t *testing.T) {
	t8 := []string{"qwen3.5:4b", "qwen2.5-coder:3b", "llama3.2:3b"}
	t16 := []string{"qwen3.5:9b", "qwen2.5-coder:7b", "qwen3:8b"}
	t32 := []string{"qwen2.5-coder:14b", "qwen3:14b", "gemma4:12b"}
	t64 := []string{"qwen3-coder:30b", "qwen3.5:27b", "qwen2.5-coder:32b"}
	with := func(fit string, groups ...[]string) []rec {
		var out []rec
		for _, g := range groups {
			for _, n := range g {
				out = append(out, rec{n, fit})
			}
		}
		return out
	}
	cat := func(parts ...[]rec) []rec {
		var out []rec
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	// "qwen3:8b:latest" exercises the ":latest" normalisation.
	installed := []Installed{{Name: "qwen2.5-coder:3b"}, {Name: "qwen3:8b:latest"}, {Name: "qwen3.5:9b"}, {Name: "llama3.2:latest"}}

	tests := []struct {
		name string
		hw   Hardware
		want []rec
	}{
		{"M1 8 GB", Hardware{AppleSilicon: true, RAMBytes: 8 << 30},
			cat(with(FitComfortable, t8), with(FitTooLarge, t16, t32, t64))},
		{"16 GB", Hardware{AppleSilicon: true, RAMBytes: 16 << 30},
			cat(with(FitComfortable, t16, t8), with(FitTooLarge, t32, t64))},
		{"32 GB", Hardware{AppleSilicon: true, RAMBytes: 32 << 30},
			cat(with(FitComfortable, t32, t16, t8), with(FitTooLarge, t64))},
		{"64 GB", Hardware{AppleSilicon: true, RAMBytes: 64 << 30},
			cat(with(FitComfortable, t64, t32, t16, t8))},
		{"128 GB", Hardware{AppleSilicon: true, RAMBytes: 128 << 30},
			cat(with(FitComfortable, t64, t32, t16, t8))},
		{"Intel 32 GB", Hardware{AppleSilicon: false, RAMBytes: 32 << 30},
			cat(with(FitComfortable, t8), with(FitTooLarge, t16, t32, t64))},
		{"4 GB", Hardware{AppleSilicon: true, RAMBytes: 4 << 30},
			cat([]rec{{"qwen3.5:4b", FitTooLarge}, {"qwen2.5-coder:3b", FitComfortable}, {"llama3.2:3b", FitComfortable}},
				with(FitTooLarge, t16, t32, t64))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Recommend(tt.hw, installed)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d recommendations, want %d", len(got), len(tt.want))
			}
			for i, r := range got {
				if r.Name != tt.want[i].name || r.Fit != tt.want[i].fit {
					t.Errorf("[%d] = %s/%s, want %s/%s", i, r.Name, r.Fit, tt.want[i].name, tt.want[i].fit)
				}
				wantInst := r.Name == "qwen2.5-coder:3b" || r.Name == "qwen3.5:9b" || r.Name == "qwen3:8b"
				if r.Installed != wantInst {
					t.Errorf("%s Installed = %v", r.Name, r.Installed)
				}
				if slow := strings.HasPrefix(r.Why, "CPU-only, slow: "); slow == tt.hw.AppleSilicon {
					t.Errorf("%s Why = %q", r.Name, r.Why)
				}
				if r.DownloadBytes <= 0 || r.Why == "" {
					t.Errorf("%s missing data: %+v", r.Name, r)
				}
			}
		})
	}
}

func TestNormName(t *testing.T) {
	for in, want := range map[string]string{"llama3.2:latest": "llama3.2", "llama3.2": "llama3.2", "qwen3:8b": "qwen3:8b"} {
		if got := normName(in); got != want {
			t.Errorf("normName(%q) = %q, want %q", in, got, want)
		}
	}
}

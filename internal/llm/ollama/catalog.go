package ollama

import "strings"

const gb = 1_000_000_000 // ollama.com shows sizes in decimal GB

// Fit labels.
const (
	FitComfortable = "comfortable"
	FitTight       = "tight"
	FitTooLarge    = "too large"
)

// tier is a RAM class and the models curated for it, best first.
type tier struct {
	minRAM int64
	models []entry
}

type entry struct {
	name string
	size int64 // download size from the ollama.com tags page
	why  string
}

// catalog is verified against ollama.com/library on 2026-10-20; see
// docs/kb/local-models.md. Updating models is a change to this table only.
var catalog = []tier{
	{8 << 30, []entry{
		{"qwen3.5:4b", 4.0 * gb, "newest small Qwen; strong code reading and JSON output (disable thinking for speed)"},
		{"qwen2.5-coder:3b", 1.9 * gb, "code-specialised and light; reliable JSON at a small size"},
		{"llama3.2:3b", 2.0 * gb, "well-tested general model; dependable fallback"},
	}},
	{16 << 30, []entry{
		{"qwen3.5:9b", 7.6 * gb, "best all-round mid-size model; good code analysis and structured output"},
		{"qwen2.5-coder:7b", 4.7 * gb, "code-specialised 7B; fast and accurate on JS/TS snippets"},
		{"qwen3:8b", 5.2 * gb, "solid general 8B with tool and JSON support"},
	}},
	{32 << 30, []entry{
		{"qwen2.5-coder:14b", 9.0 * gb, "code-specialised 14B; noticeably better reasoning about code"},
		{"qwen3:14b", 9.3 * gb, "general 14B with strong instruction following and JSON output"},
		{"gemma4:12b", 8.0 * gb, "current Gemma; good reasoning with a long context"},
	}},
	{64 << 30, []entry{
		{"qwen3-coder:30b", 19 * gb, "current Qwen coding model; mixture-of-experts, fast for its size"},
		{"qwen3.5:27b", 20 * gb, "large dense Qwen; top quality for code review"},
		{"qwen2.5-coder:32b", 20 * gb, "proven 32B coder; very reliable JSON"},
	}},
}

// Fit estimates whether a model of sizeBytes runs well with ramBytes of
// memory: resident size ≈ file size × 1.2 (KV cache and runtime overhead).
func Fit(sizeBytes, ramBytes int64) string {
	resident := sizeBytes * 12 / 10
	switch {
	case resident*100 <= ramBytes*60:
		return FitComfortable
	case resident*100 <= ramBytes*80:
		return FitTight
	default:
		return FitTooLarge
	}
}

// Recommendation is one catalogue model judged against this machine.
type Recommendation struct {
	Name          string
	DownloadBytes int64
	Why           string
	Fit           string // FitComfortable | FitTight | FitTooLarge
	Installed     bool
}

// Recommend lists catalogue models for hw: the machine's RAM tier and the
// tiers below it, best first, then the larger tiers marked FitTooLarge so a
// UI can show them greyed out. Intel Macs are capped at the smallest tier.
func Recommend(hw Hardware, installed []Installed) []Recommendation {
	have := make(map[string]bool, len(installed))
	for _, m := range installed {
		have[normName(m.Name)] = true
	}

	top := 0
	for i, t := range catalog {
		if hw.RAMBytes >= t.minRAM {
			top = i
		}
	}
	if !hw.AppleSilicon {
		top = 0
	}

	var recs []Recommendation
	add := func(e entry, fit string) {
		why := e.why
		if !hw.AppleSilicon {
			why = "CPU-only, slow: " + why
		}
		recs = append(recs, Recommendation{
			Name: e.name, DownloadBytes: e.size, Why: why, Fit: fit,
			Installed: have[normName(e.name)],
		})
	}
	for i := top; i >= 0; i-- {
		for _, e := range catalog[i].models {
			add(e, Fit(e.size, hw.RAMBytes))
		}
	}
	for i := top + 1; i < len(catalog); i++ {
		for _, e := range catalog[i].models {
			add(e, FitTooLarge)
		}
	}
	return recs
}

// normName treats "name" and "name:latest" as the same model.
func normName(s string) string {
	return strings.TrimSuffix(s, ":latest")
}

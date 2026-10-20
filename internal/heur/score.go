package heur

import "strings"

// Scoring (decision D-017). Two kinds of rules:
//
//   - Capability rules record what an extension *can* do: dynamic eval,
//     spawning processes, minified or obfuscated code, reading the
//     environment. Large legitimate extensions do all of these, so together
//     they add at most CapabilityCap points (TrustedCapabilityCap for
//     publishers in TrustedPublishers).
//   - Indicator rules record evidence of intent: hidden payloads, credential
//     reads, exfiltration hosts, download-and-run chains, typosquats. Each
//     rule contributes its top severity weight, up to +50% for repeats, and
//     rules combine as 1-Π(1-p) so every extra indicator raises the score
//     with diminishing returns. Indicators are never discounted for trusted
//     publishers: a hijacked publisher account is the supply-chain case.
//
// Final score = capability part + indicator part, clamped to 0..100.
const (
	CapabilityCap        = 35
	TrustedCapabilityCap = 15
)

// capabilityRules are rule names that describe capability, not intent.
// Every "obf_" rule is a capability rule as well.
var capabilityRules = map[string]bool{
	"eval_dynamic":          true,
	"child_process_shell":   true,
	"env_dump":              true,
	"dynamic_require_net":   true,
	"plain_http_beacon":     true,
	"unsigned_gallery":      true,
	"activation_star":       true,
	"activation_startup":    true,
	"stale_extension":       true,
	"unknown_source":        true,
	"native_binary_present": true,
	"telemetry_fingerprint": true,
}

// IsCapability reports whether rule describes capability rather than intent.
func IsCapability(rule string) bool {
	return capabilityRules[rule] || strings.HasPrefix(rule, discountPrefix)
}

func score(ws []weighted, trusted bool) int {
	type acc struct{ top, n int } // top effective weight in halves, count
	per := map[string]*acc{}
	for _, w := range ws {
		h := w.Severity.Weight() * w.halves
		if h == 0 {
			continue
		}
		a := per[w.Rule]
		if a == nil {
			a = &acc{}
			per[w.Rule] = a
		}
		a.top = max(a.top, h)
		a.n++
	}
	capability, miss := 0.0, 1.0
	for rule, a := range per {
		pts := float64(a.top) / fullWeight
		if IsCapability(rule) {
			capability += pts
			continue
		}
		pts *= 1 + 0.25*float64(min(a.n-1, 2)) // repeats add up to +50%
		miss *= 1 - min(pts, 100)/100
	}
	limit := float64(CapabilityCap)
	if trusted {
		limit = TrustedCapabilityCap
	}
	total := min(capability, limit) + 100*(1-miss)
	return max(0, min(100, int(total+0.5)))
}

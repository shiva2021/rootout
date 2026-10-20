package heur

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
)

// Rescore re-runs the extension-level rules with registry metadata attached
// (the --online path) and recomputes the score. File rules are not re-run,
// so no files are read again. Findings already present are not duplicated.
func Rescore(_ context.Context, r Report, m *Marketplace) Report {
	if m == nil {
		return r
	}
	_, extRules := snapshotRules()
	ec := &ExtCtx{Ext: &r.Ext, Files: r.files, Marketplace: m}
	seen := make(map[string]bool, len(r.Findings))
	for _, f := range r.Findings {
		seen[findingKey(f)] = true
	}
	trusted := TrustedPublishers[strings.ToLower(r.Ext.Publisher)]
	found := slices.Clone(r.weighted)
	for _, rule := range extRules {
		for _, f := range safeExtCheck(rule, ec) {
			if f.Rule == "" {
				f.Rule = rule.Name
			}
			if seen[findingKey(f)] {
				continue
			}
			seen[findingKey(f)] = true
			found = append(found, weighted{Finding: f, halves: multiplier(f.Rule, trusted, false)})
		}
	}
	out := r
	out.weighted = found
	out.Findings = make([]Finding, 0, len(found))
	for _, w := range found {
		out.Findings = append(out.Findings, w.Finding)
	}
	slices.SortStableFunc(out.Findings, func(a, b Finding) int {
		return cmp.Or(
			cmp.Compare(b.Severity, a.Severity),
			cmp.Compare(a.Rule, b.Rule),
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
		)
	})
	out.Score = score(found, trusted)
	return out
}

func findingKey(f Finding) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", f.Rule, f.File, f.Line, f.Detail)
}

// safeExtCheck runs one rule, turning a panic into a rule_panic finding.
func safeExtCheck(rule ExtRule, ec *ExtCtx) (out []Finding) {
	defer func() {
		if v := recover(); v != nil {
			out = []Finding{{Rule: ruleRulePanic, Severity: Info, Detail: fmt.Sprintf("rule %s panicked: %v", rule.Name, v)}}
		}
	}()
	return rule.Check(ec)
}

# Extension heuristics

Read before adding or tuning a rule in `internal/heur`.

## Layout
- `types.go` — Finding, Report, FileCtx/ExtCtx, rule registries, TrustedPublishers.
- `engine.go` — walker (caps: 2 MB/file, 50 MB or 2000 files/extension; never follows symlinks; never executes anything), runs rules, recovers rule panics.
- `score.go` — scoring model (D-017). `rescore.go` — re-runs extension rules with `--online` registry data.
- `rule_code.go` (A), `rule_exfil.go` (B), `rule_meta.go` + `popular.go` (C), `rule_hygiene.go` + `advisories.go` (D).
- `corpus_test.go` — golden corpus; must stay green.

## Adding a rule
1. Register in `init()` with a stable snake_case name. Obfuscation-style rules start with `obf_`.
2. Decide capability vs intent. Capability rules go in `capabilityRules` in `score.go`; anything else is an indicator.
3. When a rule pairs two patterns, require them within a byte window (see `codeChainWindow`), never "same file": minified bundles put the whole extension on one line.
4. Cap findings per file (5) and give each a `Detail` a user can act on.
5. Tests: at least two positives and two negatives, including a negative taken from a real legitimate bundle if the rule is noisy. Add a corpus case for any new attack class.
6. Run `rootout scan --all` on a real machine and look at every finding above Low before committing.

## Calibration targets
- Benign large bundles from trusted publishers: under 30.
- Corpus malicious samples: 50 or more (typosquat and stale/vulnerable: 30 or more).

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

## Performance
A real machine has ~30 MB of minified JS; Go's regexp is linear but slow
(tens of MB/s), so one whole-file `FindAllIndex` over a 2 MB bundle costs
0.1–0.3 s and the scan time is the number of such passes.
- Whole-file regex passes are the enemy. Gate a rule with `c.HasAny(lits...)`
  and run the regex with `c.FindAround(re, window, litHits, lits...)`: it
  searches `Lower()` (or `Data` for a literal with an upper-case byte, such
  as "Function") with `bytes.Index` and runs `re` only in ±window bytes around
  each hit. Every match of `re` must contain one of the literals, and
  `window` must be at least twice the longest possible match, or matches are
  lost. Split a pattern list so each regex gets only the literals its matches
  must contain; a top-level alternation needs one literal per branch.
- Never write a counted repetition like `{200,}` or an unbounded `[^\n]*`
  when a linear byte scan will do (`codeLongStrings`, `codeByteRuns`,
  `codeEscapeAt`).
- Cheap checks before expensive ones: a position comparison before a
  1.5 KB `codeNear`, a `bytes.Contains` before a regex.
- Profile: put a throwaway test that calls `heur.ScanAll` on the real
  `~/.vscode/extensions` in the package, then
  `go test -run TestTimingReal -cpuprofile /tmp/cpu.out ./internal/heur/` and
  `go tool pprof -top -cum -nodecount=80 /tmp/cpu.out | grep heur.`; the
  rules at the top of the cumulative list are the ones to convert. Before
  changing anything, save `rootout scan --all --json` and diff it afterwards:
  findings must be identical. Target: under 4 s for a dozen extensions.

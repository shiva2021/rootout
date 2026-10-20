# Handover

_Updated: 2026-10-20 08:40 · Session: planning + M0–M2_

## Status
M0, M1 and M2 code complete and green (`make test`, `make lint`). `purge list`, bare `purge` (TUI picker) and `purge remove` (dry-run verified on real apps; live removal not yet exercised) work. M2 task 7 (manual QA on a throwaway account) is pending. M3 (extension scanner) not started.

## Done this session
- Plan (`docs/PLAN.md`), decisions D-001…D-016, knowledge base.
- Packages: `fsx`, `license`, `run`, `safety`, `testutil`, `output`, `app` (Discover/Fuzzy/Resolve), `brew`, `trash`, `priv`, `leftover`, `plan`, `tui` (picker + confirm).
- Commands: `purge list [--system --search --json]`, `purge` (picker; prints choice until remove is wired into the TUI flow), `purge remove <app> [--dry-run --permanent --yes --dotfiles --no-crash-reports --no-brew --sudo --also <path>]`.
- Real-machine dry run: Spotify → 9 paths / 3.8 GB; Mail refused (apple bundle id); "Epson" reports ambiguity with candidates.

## In progress (exact state)
- Nothing mid-flight. Last change: helper-id prefix matching for HTTPStorages/WebKit/Saved State/Cookies and `/Library/Logs/DiagnosticReports` row; root-owned crash reports default to unselected.

## Next steps
1. Wire the bare `purge` flow end to end: picker → plan.Build → tui.Confirmer → plan.Execute (today it prints the chosen app). ~30 lines in `cmd/purge/tui.go`.
2. M2 task 7: run `docs/kb/qa.md` on a throwaway user account against a cask, an App Store app and a pkg app; log results there.
3. M3: define `internal/heur` and `internal/ext` types, then dispatch ext.Enumerate/Uninstall, heur engine, rule groups A–D, registry (see `docs/PLAN.md` M3).
4. Create the private GitHub repo and push (needs Shivang's go-ahead).

## Blockers and open questions
- Push to GitHub: waiting for explicit go-ahead.
- `sfltool dumpbtm` blocks without Full Disk Access; login items are probed with a 3 s timeout and otherwise silently skipped. Decide later whether to drop the probe or ask for FDA.
- Apple Developer account needed only at M5.

## Decisions this session
- D-001 … D-016 (see docs/DECISIONS.md).

## How to verify
```bash
make build && make test && make lint && scripts/toolchain_check.sh
./bin/purge list | tail -1                      # "47 applications" on this Mac
./bin/purge remove Spotify --dry-run            # lists bundle + leftovers, no changes
./bin/purge remove Mail --dry-run               # refuses: apple_bundle_id
```

## Files touched
- Everything under the repo; first session.

## Gotchas
- Homebrew's Go is 1.27.2; go.mod asks for toolchain go1.27.3 and auto-downloads it.
- Charm v2 lives at `charm.land/...`; `tea.WithAltScreen` is gone (set `view.AltScreen = true` in View); `tea.KeyPressMsg{Code, Text, Mod}`.
- `/usr/bin/trash` rejects `--` and Homebrew's `trash` is incompatible; `trash.Mover` targets the system binary by absolute path.
- macOS has no `timeout(1)`; use `perl -e 'alarm N; exec @ARGV' -- cmd` in scripts.
- gosec G204/G304 are excluded by design: argv-only exec lives in `internal/run`, and every deleted path passes `internal/safety`.

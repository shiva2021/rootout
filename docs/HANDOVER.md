# Handover

_Updated: 2026-10-22 · Session: M5 release pipeline (D-030…D-032)_

## Status
M5 is built and committed on `main`; the first release through it (v0.4.0) is **waiting on one manual step**: the Actions secret `TAP_GITHUB_TOKEN` does not exist yet. Everything else is in place: GoReleaser config, `CI` and `Release` workflows, `scripts/release_notes.sh`, `make release-check` / `make snapshot`, runbook `docs/kb/release.md`, README Install section, private tap repo `shiva2021/homebrew-rootout` (created, README only). `CI` is green on GitHub (macOS, 1m18s). The `0.4.0` CHANGELOG section is committed and pushed (`5a08a0b`) and `make release-check NEXT=v0.4.0` passes. Spec and plan: `docs/plans/m5-release.md` (Tasks 1–5 done, Task 6 from step 1b, Task 7 pending).

## Done this session (M5, D-030…D-032)
- Decisions logged first: D-030 signing deferred (cask clears quarantine), D-031 private repo + private tap with GoReleaser's token download strategy, D-032 release on every `v*` tag with CHANGELOG notes and `make release-check` as the test gate.
- `scripts/release_notes.sh` + `cmd/rootout/release_notes_test.go` (7 cases). gosec G204 added to the test-file exclusion in `.golangci.yml`.
- `.goreleaser.yaml`: darwin amd64/arm64, `-X main.version=v{{ .Version }}`, changelog disabled, `homebrew_casks` to the private tap. **Deviation:** the live GoReleaser docs show `hooks.post.install_steps` and `{{ .StagedPath }}`; 2.18.3 rejects both, so the hook uses `hooks.post.install` with `#{staged_path}` as in the docs at the v2.18.3 tag. The download strategy is verbatim from those docs. The snapshot renders the cask without `TAP_GITHUB_TOKEN`, so the Makefile has no placeholder.
- `make snapshot` verified: two archives, `checksums.txt`, arm64 binary prints `rootout version v0.3.2-next`, amd64 is x86_64 Mach-O, `dist/homebrew/Casks/rootout.rb` passes `ruby -c`.
- Workflows pass `actionlint`. GoReleaser 2.18.3 and actionlint installed locally via Homebrew.

## Next steps
1. **Shivang:** create a fine-grained token on github.com (Settings › Developer settings › Fine-grained tokens): owner shiva2021, repository `homebrew-rootout` only, Contents read and write, one-year expiry. Then `gh secret set TAP_GITHUB_TOKEN --repo shiva2021/rootout` and paste it.
2. Cut v0.4.0 (plan Task 6 steps 3–4): `git tag -a v0.4.0 -m "v0.4.0: release pipeline (GoReleaser, CI, private Homebrew tap)"`, `git push origin v0.4.0`, watch the `Release` run, check the release assets/notes and `Casks/rootout.rb` in the tap.
3. Install from the tap on this Mac (Task 6 steps 5–6): needs `HOMEBREW_GITHUB_API_TOKEN` with read access to `shiva2021/rootout`; then `rm ~/.local/bin/rootout` so it does not shadow the brew copy. If the download fails in the strategy class, apply the `GitHub.get_release` fallback from `docs/kb/release.md` and cut v0.4.1.
4. Earlier manual checks still open (unchanged from last session): model grid and home loop in a real terminal; Full Disk Access denied path; v0.2 interactive flows; M2 QA on a throwaway account; live `scan --llm`.
5. M6 membership gate: write its plan (D-024).

## Done before this session
### Model picker grid (D-029), tagged v0.3.1
- Shivang found the "Recommended for this Mac" table cluttered. The picker (`internal/tui/models.go`) and `rootout config models` now share one grid: MODEL · SIZE · FIT · NOTES with a header row; too-large models sit under "Need more memory than this Mac has"; the picker shows the selected model's description on a detail line above the flash line (plain output keeps it as a WHY column).
- New row kinds `mrSubheading`/`mrColumns` and `modelRowKind.selectable()`; scrolling up keeps the headings above the cursor visible.
- Tests: `TestModelPickerDetailLine`, `TooLargeGrouped`, `ScrollKeepsHeading`, plus header/grouping checks in `ReadyView`. `make test`/`make lint` green; renders checked at 80 and 60 columns via a throwaway test, and `./bin/rootout config models` on this Mac.
- Committed (`562fbc2`), tagged `v0.3.1`, pushed with `main`, installed with `make install`. Not yet done: a look in a real terminal (see Next steps 1).

### Versioning (D-028)
- First release tag `v0.3.0` on `main` (annotated), pushed with `main`. Convention: tag every merge (minor = feature, patch = fix/docs) at wrap-up — see `docs/kb/conventions.md` "Releases and versions".
- Version visible in `rootout --version`, the first `doctor` line (`rootout v0.3.0`, also in `--json`), the home-screen title (`rootout v0.3.0`) and `make install`'s last line. `CHANGELOG.md` added (Keep a Changelog); add to "Unreleased" as features land.
- Checking you have the latest: `rootout --version` should equal `git describe --tags` on `main`; a `-N-g<hash>` suffix means N commits past the tag, `-dirty` means uncommitted changes.

### Home screen and ctrl+x (D-027)
- Home screen `internal/tui/home.go` (`PickAction`, `Flash`): four action rows (uninstall, orphans, scan, model) plus a flash line with the last outcome.
- `tui.ErrQuit`: ctrl+x and ctrl+c quit from every screen via `quitAll`; esc/q only end the current screen (ok=false) and the caller decides what back means.
- `runTUI` home loop in `cmd/rootout/tui.go`: bare `rootout` opens home, each action returns to it with a flash summary.
- Extracted `findOrphans` and `scanReports` from `runOrphans`/`runScan`; added `declinedOnQuit` so ctrl+x and esc give the same exit code (3 for remove/orphans/config, 0 for scan). The scan assessor maps `ErrQuit` too.
- Removed the picker's orphans row (`OrphansRow`/`Pick.Orphans`). `scan --uninstall <id>` no longer scores or does `--online` lookups first.
- Help text, README and kb (architecture, conventions) updated. `guide.go` needed no change.
- Interrupt/SIGTERM quits the home loop (`runErr`, `ActionNone` guard); scan assessments are cancelled when the screen closes.

### Full Disk Access detection (D-026)
- Shivang granted Warp Full Disk Access and the stray `~/Library/Containers/zz.rootout.probe` could then be removed: FDA for the terminal app is confirmed as the remedy for protected container entries.
- New `internal/tcc`: `FullDiskAccess(env)` lists `~/Library/Safari` → `Mail` → `Application Support/com.apple.TCC` (permission error = denied, success = granted, none exist = unknown); `NeedsFullDiskAccess(env, path)` (direct child of `~/Library/Containers`); `TerminalApp(getenv)` from `TERM_PROGRAM`.
- `doctor` (and `guide`) show a "Full Disk Access" line: `granted to Warp` here; denied gives a `!` with `System Settings › Privacy & Security › Full Disk Access → add <terminal>`.
- `plan.MarkAccess(env, p)`: when denied, affected items get the note `needs Full Disk Access` and `Plan.Warning` (in `--json` too) says how many and which terminal to grant. `plan.Build` calls it; `rootout orphans` calls it after `orphanPlan`. Plain confirm prints `! <warning>` above the totals; the TUI review shows a yellow `[needs Full Disk Access]` tag per row and the warning on the flash line (a flash wins while showing). The run is never blocked.
- `.golangci.yml`: gosec G302 excluded in tests (a test chmods a dir to 0 and restores it).
- Verified on this Mac with FDA granted: `doctor` line, `make test` (24 packages), `make lint` clean, `orphans --dry-run` lists 15 `~/Library/Containers` entries that would be tagged. **The denied path has only run in unit tests** (see Next steps 1).

### Model uninstall
- `ollama.Client.Delete` (`DELETE /api/delete`, `ErrModelNotFound`), verified against docs.ollama.com.
- Picker: `d` on an installed row → `Uninstall X (size)? y/n` → spinner → re-list → flash; recommended rows show `installed` / `not installed`; `?` overlay and footer updated. `ModelDeps.ClearDefault` clears the saved default when that model goes.
- `rootout config models rm <model> [--yes]`; first tests in `cmd/rootout` (`config_test.go`, exercises `removeModel` with an httptest Ollama and a temp config).
- Verified on this Mac: `rm ghost:1b --yes` → not-found error, exit 1; `echo n | rm qwen3.5:4b` → cancelled, exit 3, config untouched. Did not run a real delete (only one model installed).

### Orphan removal bug
- Shivang ran `rootout orphans` for real: 25 paths, 0 removed, every one reported FAILED with the same error about `~/Library/Containers/ru.keepcoder.Telegram`. Cause: `/usr/bin/trash -s` stops at the first failure, so one unmovable path sank the whole batch and `trash.Move` marked all 25 failed. Fix: a failed batch is retried one path at a time (`internal/trash`), so only the truly unmovable path fails. Verified with the real tool: 2 scratch files moved, the protected dir failed alone.
- Why the Telegram container could not be moved: macOS protects the entries directly under `~/Library/Containers`. Owner/mode/flags are normal, reading and writing *inside* works, but unlink/rename/trash of the entry itself is denied (`afpAccessDenied`, -5000) for the user, for `/usr/bin/trash` and for Finder via osascript. `~/Library/Group Containers` was not probed. The error now carries a hint to grant the terminal Full Disk Access. The `writable()` check (W_OK on the parent) cannot see this, so such paths are classified as user paths.

### v0.2
- Progress: `internal/progress`; hooks in app discovery (4 size workers), leftover search, extension scan, removal; picker loads inside the program.
- Typed `delete` in the confirm and scan screens and the plain prompt; `--yes` is the only bypass; `scan --uninstall <id> [--yes]`.
- Help: `Long`/`Example` everywhere, `rootout guide`, first-run hint, `?` overlays.
- Scan screen: `AI:` header, `m` embeds the model picker, `scan --pick-model`.
- `rootout orphans`: `leftover.FindOrphans` (reverse-DNS ids only; owned via installed apps, Apple, SDK list, Spotlight, launchd, processes; vendor-still-installed annotation). On this Mac: 29 candidates, all plausible (old Telegram, ChatGPT, 1Password 7, RStudio, DaVinci…); nothing under com.apple.
- Scan performance: 31 s → 5 s on this Mac (`FindAround`, literal gates, no counted repetitions).

### Earlier
- Plan (`docs/PLAN.md`), decisions D-001…D-017, knowledge base.
- Apps: `rootout` (v0.2: picker → review → remove; now the home screen, see above), `list`, `remove` (Trash/permanent/dry-run, brew zap, one admin elevation, receipts, launchd bootout).
- Extensions: `scan` (5 editors, 4 rule groups, capability/intent scoring, `--online` registry data, review screen with uninstall), golden corpus.
- AI: Anthropic and OpenAI via official SDKs with structured JSON, Ollama via HTTP (`think:false`); keys in Keychain; `config` command; Ollama picker that detects, installs, starts, recommends by RAM tier and pulls with progress.
- `doctor`.

## Real-machine results (this Mac: M1, 8 GB, 47 user apps, 12 VS Code extensions)
- `rootout remove Spotify -n`: bundle + 7 leftovers, 3.8 GB; `Mail` refused.
- `rootout scan --all`: scores 5–30. The 30 is `ms-python.vscode-python-envs`, which pipes the official uv installer from astral.sh into `sh` (true finding, Medium).
- Review screen and app picker verified under a pseudo-terminal (render + quit). Live removal and live LLM calls have not been run.

## Blockers and open questions
- Unsigned binary relies on the cask's quarantine-clearing hook (D-030); a future macOS may block it. Signing via the D-015 quill flow is the fix.
- `TAP_GITHUB_TOKEN` expires after a year; rotation steps in `docs/kb/release.md`.
- Intel Macs: `ollama.Recommend` caps them at the 8 GB tier, so bigger tiers are marked too large and now sit under "Need more memory than this Mac has", which is wrong for a large-RAM Intel Mac. Pre-existing semantics, not changed by D-029.
- `Group Containers` is not probed or tagged by D-026; nobody has checked whether its entries carry the same TCC protection as `~/Library/Containers`. Test only with a throwaway entry and the Finder GUI as fallback.
- `docs/research/vscode-extension-audit/` names Shivang's LeetCode username; remove it before the repo ever goes public.
- Binary is 26 MB stripped (was 9 MB before the AI wiring). The official SDKs (D-013) are a few MB of that; replacing them with plain HTTP would shrink it but reverses D-013. Shivang to decide.
- `sfltool dumpbtm` blocks without Full Disk Access; login items probe has a 3 s timeout and is otherwise skipped.
- The VSIX signature file name is unverified; `unsigned_gallery` is Low for that reason (all 12 local extensions have `.vsixmanifest` but none of the guessed signature files).

## How to verify
```bash
make build && make test && make lint && scripts/toolchain_check.sh
make release-check NEXT=v0.4.0 && make snapshot
./bin/rootout doctor
./bin/rootout remove Spotify --dry-run
./bin/rootout scan --all --no-tui
```

## Gotchas
- Scan speed: never run a regex over a whole bundle; use `FileCtx.FindAround` with the pattern's mandatory literals and avoid `{200,}`-style repetitions (docs/kb/heuristics.md, Performance). Profile with the throwaway timing test described there.
- ctrl+x/ctrl+c must be checked before the `?` overlay close and before delegating to the typed box / embedded picker, or the quit key gets eaten.
- pty testing: keys sent before the alternate screen opens are buffered and `ESC ESC q` then parses as Alt-combinations; wait for the screen before sending keys.
- The tool was renamed from `purge` to `rootout` (D-018) because macOS ships `/usr/sbin/purge`. `make install` puts it in `~/.local/bin`.
- A failing `go test` piped into `tail` hides the exit code; one bad commit slipped through that way (fixed in the next commit). Check `${PIPESTATUS[0]}` or grep for FAIL.
- Charm v2: `charm.land/...` imports, alt screen via `view.AltScreen`, lipgloss sizes include borders.
- `/usr/bin/trash` rejects `--`; Homebrew's `trash` is a different tool.
- Minified bundles are one line: any rule pairing two patterns needs a byte window (see `docs/kb/heuristics.md`).
- macOS has no `timeout(1)`: `perl -e 'alarm N; exec @ARGV' -- cmd`.
- gosec G101 flags constant names containing "Assess"/"Key"-like words; rename rather than suppress.

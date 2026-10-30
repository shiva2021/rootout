# Handover

_Updated: 2026-10-30 · Session: M6 membership gate brainstormed, researched and planned; nothing implemented yet_

## Status
**M6 is planned, not started.** Spec and task plans are committed on branch `m6/membership` (`cad25a1`): `docs/plans/m6-membership.md` (design, Part A Go CLI tasks 1–7, Part C public flip) and `docs/plans/m6-service.md` (Cloudflare Worker tasks S1–S6). Shivang approved the design on 2026-10-30; the implementation plans await his review and a choice of execution method (subagent-driven vs native). Work happens on `m6/membership` and merges to `main` for the v0.5.0 tag. Summary of the design: patrons paste a machine-bound Ed25519 licence key issued by a Worker after Patreon login; 7-day trial once per Mac; `list`/`doctor`/`guide`/`--dry-run`/heuristic `scan` stay free; deletion and AI need a key; admin keys are minted offline per Mac with `cmd/rootout-keys`; keys live 35 days, renew silently, 14-day grace only when renewal fails; two Macs per account; Worker on workers.dev; alerts via GitHub issue. GoReleaser 2.19.0 is stable now (released 2026-10-22, has `install_steps`) and is Task 1. Patreon research (2026-10-30) is summarised in the spec; the decisive fact is that Patreon's token exchange needs the client secret server-side, so a backend is unavoidable.

M5 is done. Pushing a `v*` tag now builds, publishes a GitHub Release with CHANGELOG notes and updates the private tap. **v0.4.1** is the first complete release: two archives, checksums, notes, and `Casks/rootout.rb` in `shiva2021/homebrew-rootout` with matching sha256s. Installed on this Mac from the tap (`/opt/homebrew/bin/rootout`, `v0.4.1`, no quarantine attribute, `doctor` runs); the old `~/.local/bin/rootout` (v0.3.1) is removed. v0.4.0 is on GitHub Releases without a cask (first token lacked tap access) and its notes were backfilled by hand. CI green on macOS. Spec and plan: `docs/plans/m5-release.md`; runbook: `docs/kb/release.md`.

## Done this session (M5, D-030…D-032)
- First releases: v0.4.0 published but the tap update got 404 (token lacked access to `homebrew-rootout`) and its body was empty; v0.4.1 fixed both. Empty body cause: in GoReleaser 2.18.3 `changelog.disable` skips the pipe that loads `--release-notes`, so the setting is gone (D-032 wording updated).
- Tap install verified with `HOMEBREW_GITHUB_API_TOKEN=$(gh auth token)`. Newer Homebrew needs `brew trust shiva2021/rootout` before it loads the cask; README and runbook updated.
- Decisions logged first: D-030 signing deferred (cask clears quarantine), D-031 private repo + private tap with GoReleaser's token download strategy, D-032 release on every `v*` tag with CHANGELOG notes and `make release-check` as the test gate.
- `scripts/release_notes.sh` + `cmd/rootout/release_notes_test.go` (7 cases). gosec G204 added to the test-file exclusion in `.golangci.yml`.
- `.goreleaser.yaml`: darwin amd64/arm64, `-X main.version=v{{ .Version }}`, changelog disabled, `homebrew_casks` to the private tap. **Deviation:** the live GoReleaser docs show `hooks.post.install_steps` and `{{ .StagedPath }}`; 2.18.3 rejects both, so the hook uses `hooks.post.install` with `#{staged_path}` as in the docs at the v2.18.3 tag. The download strategy is verbatim from those docs. The snapshot renders the cask without `TAP_GITHUB_TOKEN`, so the Makefile has no placeholder.
- `make snapshot` verified: two archives, `checksums.txt`, arm64 binary prints `rootout version v0.3.2-next`, amd64 is x86_64 Mach-O, `dist/homebrew/Casks/rootout.rb` passes `ruby -c`.
- Workflows pass `actionlint`. GoReleaser 2.18.3 and actionlint installed locally via Homebrew.

## Next steps
0. **M6**: Shivang reviews `docs/plans/m6-membership.md` and `m6-service.md`, picks the execution method, then execute Part A Task 1 onwards on `m6/membership`. Task 4 and S6 need Shivang at the keyboard (keypair generation and backup, Cloudflare and Patreon accounts, secrets). The public flip (Part C) is a separate patch tag after v0.5.0.
1. `make install` now puts a second `rootout` in `~/.local/bin`, which comes first on `PATH` and hides the brew copy. Use it only for testing a branch and remove it afterwards (`docs/kb/release.md`, "Which binary am I running").
2. Earlier manual checks still open: model grid and home loop in a real terminal; Full Disk Access denied path; v0.2 interactive flows; M2 QA on a throwaway account; live `scan --llm`.
3. M6 membership gate: write its plan (D-024). It decides how members get the binary, since the tap and releases are private. **Agreed on 2026-10-22, deferred to M6:** drop the token requirement by publishing to a public releases repo; recipe below ("Public releases repo").
4. Optional later: signing and notarization (D-030 / D-015 quill flow).
5. When GoReleaser 2.19 is released as stable (only nightlies on 2026-10-22): bump the pin in `.github/workflows/release.yml` (`version: v2.19.x`) and locally (`brew upgrade goreleaser`, `docs/kb/setup.md`); in `.goreleaser.yaml` replace `hooks.post.install` with `install_steps` (`on_macos do run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{ .StagedPath }}/rootout"] end`); `make snapshot` and check the cask has `postflight_steps`; cut a patch release. Why: Homebrew 7.0.10 warns "Calling `postflight` is deprecated! Use `postflight_steps` instead." on every command that loads the cask. Install still works; Homebrew will eventually remove `postflight`. Shivang chose to wait for 2.19 over hand-writing the block in `custom_block` or using a nightly.

## Public releases repo (planned 2026-10-22, execute at M6)
**Why.** `brew install --cask rootout` fails without `HOMEBREW_GITHUB_API_TOKEN` because `shiva2021/rootout` is private and GitHub will not serve its release assets anonymously (D-031). Shivang does not want new users to deal with a token. Until then, reinstalling on this Mac is `HOMEBREW_GITHUB_API_TOKEN=$(gh auth token) brew install --cask rootout`.

**Design (Shivang approved the defaults).** Source stays private. Release assets go to a new **public** repo `shiva2021/rootout-releases`; the tap `shiva2021/homebrew-rootout` keeps its name and becomes public. The cask is then a plain `CurlDownloadStrategy` cask: no token, no custom Ruby class, no Homebrew internals. The paywall lives in the binary (M6 gate, D-024). Not done now because a public binary before the gate is a free rootout, and building it while the repos are private would 404 every `brew install/upgrade`. Log it as D-033 when executing.

**Verified against GoReleaser 2.18.3 (docs + `internal/client/github.go`, `internal/pipe/release/release.go`):**
- `release.github.owner/name` picks the repo the release is created in; `release.token` (since v2.17) is that repo's token. The cask's default download URL is built from `Release.GitHub.Owner/Name` (`ReleaseURLTemplate`), so no `url.template` is needed.
- Actions' default `GITHUB_TOKEN` cannot write to another repo. One fine-grained PAT with Contents read/write on `rootout-releases` + `homebrew-rootout`, stored as `RELEASE_GITHUB_TOKEN` (replaces `TAP_GITHUB_TOKEN`; one thing to rotate).
- GitHub creates the tag in `rootout-releases` at release time; that repo needs one commit first (README with the install steps).
- `brew trust shiva2021/rootout` stays necessary on Homebrew 7 for any third-party cask tap.

**Steps.**
1. Create `shiva2021/rootout-releases` (README = install steps); make it and `homebrew-rootout` public.
2. Edit the fine-grained token to cover both repos; `gh secret set RELEASE_GITHUB_TOKEN --repo shiva2021/rootout`; delete `TAP_GITHUB_TOKEN`.
3. `.goreleaser.yaml`: `release.github.name: rootout-releases`, `release.token: "{{ .Env.RELEASE_GITHUB_TOKEN }}"`. In `homebrew_casks`: remove `url` and `custom_block`; `repository.token` → the same env; `homepage` → `rootout-releases`; caveats → "not code-signed; quarantine cleared on install". Keep the post-install hook (`install_steps` once on GoReleaser ≥ 2.19, Next steps 5).
4. `.github/workflows/release.yml`: `TAP_GITHUB_TOKEN` env → `RELEASE_GITHUB_TOKEN`.
5. Docs: README install block becomes `brew tap shiva2021/rootout && brew trust shiva2021/rootout && brew install --cask rootout`; rewrite "Install from the tap", "Tokens", "Known limits" in `docs/kb/release.md`; D-033 in `docs/DECISIONS.md` (supersedes D-031's cask).
6. Verify: `make snapshot` → `dist/homebrew/Casks/rootout.rb` has a plain `url` and no custom class; cut a patch tag; `gh release view --repo shiva2021/rootout-releases`; in a shell with **no** `HOMEBREW_GITHUB_API_TOKEN`: `brew untap shiva2021/rootout`, tap, trust, install, `rootout --version`. Old releases v0.4.0/v0.4.1 stay on the private repo.

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
- The cask uses the deprecated `postflight` block (GoReleaser 2.18.3 cannot write `postflight_steps`); see Next steps 5.
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

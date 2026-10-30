# Handover

_Updated: 2026-10-30 · Session: M6 membership gate built end to end on branch `m6/membership` (not merged, not released); human checkpoints remain_

## Status
**M6 is implemented and reviewed, not shipped.** Branch `m6/membership` (31 commits on top of `main` at `b7dbb2e`) carries the whole gate: `internal/license` (Ed25519 machine-bound keys, key file + renewal stamp, service client, `Resolve` state machine), the gates in `cmd/rootout` (remove, orphans, scan uninstall, review screen, LLM, TUI home loop), the `rootout license` command, doctor and home-title status, `cmd/rootout-keys` (local signing tool), the Cloudflare Worker in `service/` (key page, `/start`, Patreon OAuth `/callback` with a 2-Mac device cap, `/renew`, `/trial`, per-IP rate limit, daily creator-token refresh, GitHub-issue alerts), the GoReleaser 2.19.0 bump with the `install_steps` cask hook, a Worker CI job, `docs/kb/membership.md`, D-033/D-034, README Membership section, `LICENSE`. Every task had a fresh implementer, a task review and fix rounds; a whole-branch review ended "merge with fixes" and the fix wave was re-reviewed clean. `make test`, `make lint`, `go test -race` (cmd, license, tui), `cd service && npm test` (84) and `npm run typecheck` are green. `make release-check` **fails on purpose** until Checkpoint K fills `license.PublicKeyHex` and the real `ServiceURL` host replaces `CHANGEME` (`internal/license/client.go`).

This session ran on a different Mac from the handover's M1 (M4 Pro, 48 GB; Go 1.27.3, golangci-lint 2.15.0, GoReleaser 2.19.0 installed via Homebrew; no rootout installed from the tap here). Spec and plans: `docs/plans/m6-membership.md` (design, Part A Go tasks, Part C public flip) and `docs/plans/m6-service.md` (Worker). The SDD ledger with every ruling lives in `.superpowers/sdd/m6-membership/progress.md` (git-ignored; also summarised in the session's final message).

## Next steps (in order; the first four need Shivang at the keyboard)
1. **Checkpoint K** (`docs/plans/m6-membership.md`): `go run ./cmd/rootout-keys gen`; store the PEM and the PKCS8 base64 in the password manager as "rootout signing key"; paste the public hex into `PublicKeyHex` in `internal/license/codec.go`; commit `license: embed the production public key (D-034)`. Then Task 5 Step 12 (manual check on a Mac: `rootout license`, `remove X --dry-run` free, admin key → `doctor` shows `admin`).
2. **S6** (`docs/plans/m6-service.md`): Cloudflare account, `wrangler login`, `wrangler kv namespace create KEYS` → id into `wrangler.toml`; Patreon campaign + v2 client with redirect `https://rootout-keys.<account>.workers.dev/callback`; `wrangler secret put` × 6; seed the creator token pair (`--remote`); set `PATREON_URL`/`PUBLIC_ORIGIN`; `npm run deploy`; smoke `/trial` twice (200 then 409) and delete the trial key; paste the host into `license.ServiceURL` and `docs/kb/membership.md`. S6.6 (first real pledge from a second account) verifies the `identity` scope assumption and the gifted/declined semantics.
3. **Task 7 release**: merge `m6/membership` into `main` (Shivang's call; the branch is review-clean), move the `## [Unreleased]` changelog content under `## [0.5.0] - <date>`, `make release-check NEXT=v0.5.0`, tag, push; `HOMEBREW_GITHUB_API_TOKEN=$(gh auth token) brew upgrade --cask rootout` and `doctor | grep Membership`.
4. **Part C** (public releases repo, D-033) as a separate patch tag v0.5.1; then a token-free `brew install --cask rootout` in a clean shell; then rewrite this handover.
5. Deferred minors from the reviews, none blocking (full list in the ledger): a scan AI label computed offline shows "AI: members" for a past-exp key until the first assessment renews it; `StatusNoMachine` cached as final for a TUI session; memory rate limiter unpruned when the Worker binding is absent; alerts not debounced; a wrong `PATREON_CAMPAIGN_ID` fails silently ("not a member" for everyone); `/callback` and `/start` have no rate limit; `license <key>` refuses a just-expired patron key (the key page always issues fresh ones); `Client.Renew` reports a 400 as "service unavailable"; D-015 and `docs/PLAN.md` library rows still name GoReleaser 2.18.3 in places.
6. Earlier manual checks still open (see "Done before this session"): model grid and home loop in a real terminal; Full Disk Access denied path; v0.2 interactive flows; M2 QA on a throwaway account; live `scan --llm`. Optional later: signing and notarisation (D-030).

## Done this session (M6 build, 2026-10-30)
- Brainstormed and researched the design (Patreon API v2 facts are in the spec); builder-checked; two reviewer passes on the plan.
- Tasks 1–6 (Go), S1–S5 (Worker), 7a (docs) implemented by fresh subagents with per-task reviews; final whole-branch review + one fix wave. Key behaviours decided in the fix loops: a `/renew` 403 is remembered beside the key until a renewal succeeds or a fresh 403 replaces it; the renewal throttle is 24 h, or 1 h once past expiry with no remembered denial; a `StatusNone` (no key, trial not started) is never cached, so an offline trial start retries; warnings (grace, upcoming denial, unsaved key) print on gated paths, in flashes, in the AI label, in doctor (`!`) and the home title ("grace: renew soon"); denial reasons are shown in plain words.
- Worker tooling: `@cloudflare/vitest-plugin` 1.4.11 + vitest 4.1.12 (the plan's `vitest-pool-workers` is deprecated and rejects the 2026 compatibility date); stable `[[ratelimits]]` binding; `wrangler kv key …` needs `--remote` on Wrangler 4.
- `make contract` runs `internal/license/contract_test.go` (build tag `contract`) against `npm run dev` with `service/.dev.vars` holding the **test** signing key (local-only file, git-ignored; contents in `docs/plans/m6-membership.md` Task 6 dispatch / recreate from the seed in `service/test/sign.test.ts`).
- Commit trailers on implementer commits credit "Claude Opus 5.5" (the subagents' model); controller commits credit Fable 5.1.

## Done before this session

### M5 release pipeline (D-030…D-032), 2026-10-22
- First releases: v0.4.0 published but the tap update got 404 (token lacked access to `homebrew-rootout`) and its body was empty; v0.4.1 fixed both. Empty body cause: in GoReleaser 2.18.3 `changelog.disable` skips the pipe that loads `--release-notes`, so the setting is gone (D-032 wording updated).
- Tap install verified with `HOMEBREW_GITHUB_API_TOKEN=$(gh auth token)`. Newer Homebrew needs `brew trust shiva2021/rootout` before it loads the cask; README and runbook updated.
- Decisions logged first: D-030 signing deferred (cask clears quarantine), D-031 private repo + private tap with GoReleaser's token download strategy, D-032 release on every `v*` tag with CHANGELOG notes and `make release-check` as the test gate.
- `scripts/release_notes.sh` + `cmd/rootout/release_notes_test.go` (7 cases). gosec G204 added to the test-file exclusion in `.golangci.yml`.
- `.goreleaser.yaml`: darwin amd64/arm64, `-X main.version=v{{ .Version }}`, changelog disabled, `homebrew_casks` to the private tap. **Deviation:** the live GoReleaser docs show `hooks.post.install_steps` and `{{ .StagedPath }}`; 2.18.3 rejects both, so the hook uses `hooks.post.install` with `#{staged_path}` as in the docs at the v2.18.3 tag. The download strategy is verbatim from those docs. The snapshot renders the cask without `TAP_GITHUB_TOKEN`, so the Makefile has no placeholder. **Superseded on 2026-10-30 (M6 Task 1):** the hook now uses `install_steps` with `{{ .StagedPath }}` on GoReleaser 2.19.0.
- `make snapshot` verified: two archives, `checksums.txt`, arm64 binary prints `rootout version v0.3.2-next`, amd64 is x86_64 Mach-O, `dist/homebrew/Casks/rootout.rb` passes `ruby -c`.
- Workflows pass `actionlint`. GoReleaser (2.19.0 since 2026-10-30) and actionlint installed locally via Homebrew.

### Public releases repo (planned 2026-10-22, execute at M6)
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


## Real-machine results

_The M6 session ran on an M4 Pro (48 GB) with no rootout installed; nothing below was re-run there._

- `rootout remove Spotify -n`: bundle + 7 leftovers, 3.8 GB; `Mail` refused.
- `rootout scan --all`: scores 5–30. The 30 is `ms-python.vscode-python-envs`, which pipes the official uv installer from astral.sh into `sh` (true finding, Medium).
- Review screen and app picker verified under a pseudo-terminal (render + quit). Live removal and live LLM calls have not been run.

## Blockers and open questions
- `license.PublicKeyHex` is empty and `ServiceURL` is a placeholder: every real key fails closed and no trial can start until Checkpoint K and S6; `make release-check` enforces this.
- The Patreon `identity` scope returning our campaign's membership, and the gifted/declined field semantics, are verified from docs and forum posts only; S6.6 is the real test.
- `TAP_GITHUB_TOKEN` is replaced by `RELEASE_GITHUB_TOKEN` in Part C; until then the private-tap token story from M5 still applies.
- Unsigned binary relies on the cask's quarantine-clearing hook (D-030); a future macOS may block it. Signing via the D-015 quill flow is the fix.
- Intel Macs: `ollama.Recommend` caps them at the 8 GB tier, so bigger tiers are marked too large and now sit under "Need more memory than this Mac has", which is wrong for a large-RAM Intel Mac. Pre-existing semantics, not changed by D-029.
- `Group Containers` is not probed or tagged by D-026; nobody has checked whether its entries carry the same TCC protection as `~/Library/Containers`. Test only with a throwaway entry and the Finder GUI as fallback.
- `docs/research/vscode-extension-audit/` names Shivang's LeetCode username; remove it before the repo ever goes public.
- Binary is 26 MB stripped (was 9 MB before the AI wiring). The official SDKs (D-013) are a few MB of that; replacing them with plain HTTP would shrink it but reverses D-013. Shivang to decide.
- `sfltool dumpbtm` blocks without Full Disk Access; login items probe has a 3 s timeout and is otherwise skipped.
- The VSIX signature file name is unverified; `unsigned_gallery` is Low for that reason (all 12 local extensions have `.vsixmanifest` but none of the guessed signature files).


## How to verify
```bash
make build && make test && make lint && scripts/toolchain_check.sh
go test -race ./cmd/... ./internal/license/ ./internal/tui/
(cd service && npm test && npm run typecheck)
(cd service && npm run dev) &   # then: make contract
./bin/rootout doctor | grep Membership
./bin/rootout license
./bin/rootout remove Spotify --dry-run
```

## Gotchas
- Worker tests: `cloudflare:test` has no type declarations in this setup, so tests call the worker's `fetch` directly and inject `fetchFn`; `fetchMock` is not exported by the plugin.
- Never run `git add -A` from the repo root with `service/node_modules` present; stage explicit paths.
- `ed25519.Verify` panics on a wrong-length public key: `Decode` checks the length first and an empty `PublicKeyHex` fails closed.
- Go's `json.Marshal` HTML-escapes `<>&`; the key codec uses `json.Encoder` with `SetEscapeHTML(false)` so payload bytes equal `JSON.stringify` in the Worker.
- The SDD review-package and task-brief scripts write to `.superpowers/sdd/<plan>/`; the brief script does not parse "Task S1" headings (extract with awk).
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

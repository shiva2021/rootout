# Handover

_Updated: 2026-11-04 · Session: update check shipped as v0.8.0 and v0.8.1, verified on this Mac; session wrapped_

## Status
**v0.8.1 is the current release and the update check is live.** Shivang asked how users learn that a new release exists; nothing told them. One session took it from question to release: plan (`docs/plans/update-check.md`, PR #17), code (PR #18, D-039: `internal/update`, `updates.check`, doctor `Update` row, home-screen notice), v0.8.0 (PR #19, tag on the merge commit), the five minors a fresh whole-branch review deferred (PR #21: parsed `vX.Y.Z` on screens instead of the raw tag, unparseable tag reported, local-time date, no record when rootout's own context is cancelled, releases page from `update.ReleasesPage`; D-039 states the read-only cache directory exception), and v0.8.1 (PR #22). Every release went through `make release-check`, a release PR, the tag, and the `Release` workflow; both release pages carry two archives and `checksums.txt`, and the cask is at 0.8.1. On this Mac the brew copy is v0.8.1 and its `rootout doctor` prints `✔ Update  up to date (latest v0.8.1)`. `make test` 26 packages ok, `make lint` 0 issues, `main` clean, all working branches deleted.

How the check behaves, for the next person: doctor always asks GitHub (5 s timeout, fail-soft OK row) and writes `~/.cache/rootout/update.json`; the home screen reads that record when younger than 24 h, else asks once in the background (failures recorded too) and shows `vX.Y.Z available · <hint>` dim after the version on the title line; `dev`/`go install` builds never ask; `rootout guide` never asks (the row is added in doctor's `RunE`, not `runChecks`); `rootout config set updates.check false` turns both off.

**README has a "Releases" section** with a mermaid diagram of the release flow as the git history shows it: feature PRs → `release/X.Y.Z` branch with the CHANGELOG move → `make release-check` → release PR merged → tag on `main` → Release workflow (notes from CHANGELOG, GoReleaser) → GitHub Release and cask → `brew upgrade`. CHANGELOG Unreleased has the line. Docs only; `make test`/`make lint` green. `docs/kb/release.md` "Cut a release" now describes the PR flow (release branch, PR, tag on the merge commit) and names `install_steps` as `.goreleaser.yaml` does. No tag for these docs merges (Shivang's call); the Unreleased line rolls into the next release.

## Previous session (v0.7.0)
**v0.7.0 covers all four scan/LLM enhancements** (D-035…D-038), merged into `main` in order as PRs #10 (hosted model choice), #11 (`l` on ticked rows, bounded queue, summary pane), #12 (verdict icons), #13 (saved assessments, `L`, `--no-cache`, `config cache clear`). One tag instead of the four the plan assumed, because all four landed together; the CHANGELOG `0.7.0` section carries the five lines. Both review passes (one per stack half) are fully folded in, minors included. `make test`/`make lint` and CI on `main` green.

- **D-035**: `anthropic/openai.ListModels` (`Verify` wraps them), `providers.ListModels`, `tui.HostedPicker` + `embeddedPicker`, the wizard's model step after a verified key, `m` for hosted providers, `config models --provider anthropic|openai [--pick]`, generic `saveModel` (silent; CLI paths announce), home AI line refreshed after a scan. OpenAI ids filtered to Responses-API-capable ones.
- **D-036**: `l` queues the ticked rows (visible first, then hidden), `assessWorkers = 2`, skips answered, retries errors, hint when nothing ticked, unticked-while-queued rows not sent; "AI summary" pane when >1 ticked and one asked; footer drops entries to fit the width.
- **D-037**: ✓/✗/⚠︎(U+FE0E)/! after the editor tag from `llm.Assessment` only (`ConfidenceThreshold` 0.70); legend on the AI line and in `?`; narrow rows drop the editor tag.
- **D-038**: `internal/assesscache` (`ROOTOUT_CACHE`, else `XDG_CACHE_HOME`, else `~/.cache/rootout/assessments.json`), `llm.PromptVersion` + `Fingerprint` pinned by `TestPromptVersionBumped`, cache opened once per run in `cmd/rootout/assess.go`, preload + `L` on the scan screen, old-model answers kept only where nothing newer exists, `scan --no-cache`, `config cache clear` (refuses anything that is not a rootout cache file: the one deletion outside `safety.Validate`, stated in D-038), `config show` line. Live on this Mac: 9 Claude calls, then 9 cache hits; the real cache file now holds those 9 entries.

Verified: unit/view tests throughout (TDD), a pty run of the scan screen against a fake local model (tick → `l` → summary, icons, legend, `?`), `config models --provider anthropic` against the real key. Not run in a real terminal: the wizard's model step and `m` → hosted picker.

## Done earlier (onboarding, 2026-11-03)
- Brainstormed with Shivang: TUI wizard + `rootout setup`; status line + "Set up AI" row; live key check with save-anyway. Plan reviewed by a Plan agent and the advisor; findings folded in: key paste trimming, `CharLimit = 0`, input blurred in the error state, per-call timeout, Keychain read once per launch, empty file on skip.
- TDD throughout: `internal/llm/{anthropic,openai}/verify_test.go`, `providers_test.go`, `internal/tui/setup_test.go` (16 cases incl. the embedded picker), `home_test.go` (AI line, 30-line view), `cmd/rootout/setup_test.go` (`applySetup`, `needsSetup`, `keyVerifier`, `existingKey`, `aiStatus` with a map-backed `keychain.Store`), `config_test.go` (empty file = defaults).
- `cmd/rootout/setup.go` persists; `tui.go` runs the wizard when `needsSetup(path)` and recomputes the AI status after the setup action; `main.go` adds `setup`, skips the first-run hint for the bare command (the wizard covers it) and for `setup`; the hint and the guide's AI step name `rootout setup`; scan's "AI: none" line mentions it.
- Released v0.6.0 after Shivang merged PR #8. `brew upgrade --cask rootout` first said "latest version already installed" with v0.5.0 on disk: the local tap clone was stale; `brew update` then the upgrade fixed it (runbook updated).
- Scoped the ruleset to the default branch via `gh api -X PUT repos/shiva2021/rootout/rulesets/<id>` with `conditions.ref_name.include = ["~DEFAULT_BRANCH"]`.
- Not run: the real-key path on this Mac (`rootout setup` → Claude → enter keeps the stored key → live check) and the Ollama path with a real download. One-screen manual checks (Next steps 1).

## Done earlier today (open-source release, D-033)
- Shipped v0.5.0 and v0.5.1; token-free install verified. The tap went public only after the release had rewritten its cask.
- D-033 logged (supersedes D-008, D-024, D-031). `.goreleaser.yaml`: plain cask, `install_steps`, LICENSE in archives; workflow pin `v2.19.0`. `internal/license` deleted; exit codes 0–3.
- `REPORT.md:47` login scrubbed at HEAD (stays in history since `90ba3fb`, Shivang's call). `.gitignore`: `/service/`, `/.idea/`. Repo topics, private vulnerability reporting on.

## Next steps
1. Not yet seen by a human in a real terminal: the home-screen notice and the brew hint. Quick way: `go build -ldflags "-X main.version=v0.1.0" -o /tmp/rootout-old ./cmd/rootout && /tmp/rootout-old` shows `v0.8.1 available · github.com/…/releases` on the title (a pty run showed it); the brew hint needs the binary under `/opt/homebrew/Caskroom/rootout/`, so it appears for real users the day after the next tag.
2. Manual checks of the v0.7.0 features still not done in a real terminal: `rootout setup` → Claude → enter (keeps the stored key, live check) → model list → choose → home shows `AI: Claude · <model>`; scan → tick rows → `l` → summary and icons → `m` → hosted picker → `L`.
3. `~/.local/bin/rootout` (an old `make install` build from 2 Nov) is still on this Mac and may shadow the brew copy: `rm ~/.local/bin/rootout` (`docs/kb/release.md`, "Which binary am I running").
4. Earlier manual checks still open: model grid and home loop in a real terminal; Full Disk Access denied path; v0.2 interactive flows; M2 QA on a throwaway account; live `scan --llm`.
5. Optional: signing and notarisation (D-030 / D-015 quill flow); homebrew-cask submission once the project meets Homebrew's notability bar.
6. One-time history scan: `brew install gitleaks && gitleaks git .`.
7. Dependabot PRs #3–#5 (anthropic-sdk-go 1.84.0, openai-go 3.91.0, bubbletea 2.1.1) are still open; review, merge and tag a patch release. D-035 names the SDK fields (`ModelInfo.Lifecycle`, `Model.ShutdownDate`) a bump must keep.

## Done before this session
### M5 release pipeline (D-030…D-032), 2026-10-22
- v0.4.0 published without a cask (tap token lacked access) and with an empty body (`changelog.disable` skipped `--release-notes` in 2.18.3); v0.4.1 fixed both. Both casks need `HOMEBREW_GITHUB_API_TOKEN`; v0.5.0 is the first token-free one.
- `scripts/release_notes.sh` + `cmd/rootout/release_notes_test.go`; `make release-check` is the gate (the release job does not run tests); `make snapshot` renders the cask into `dist/`.
- Homebrew 7 needs `brew trust shiva2021/rootout` for any third-party cask tap.

### Membership gate (parked, branch `m6/membership`, 2026-10-30)
- Built end to end and review-clean but never merged: Ed25519 machine-bound keys, a Cloudflare Worker key service with Patreon OAuth, gates at the action, `rootout license`, `cmd/rootout-keys`. Dropped by D-033. If ever revisited, its own `docs/HANDOVER.md`, `docs/plans/m6-membership.md` and `docs/plans/m6-service.md` are on the branch.

### Model picker grid (D-029), v0.3.1
- Picker and `rootout config models` share one grid (MODEL · SIZE · FIT · NOTES); too-large models grouped under "Need more memory than this Mac has".

### Versioning (D-028)
- Annotated tag on every merge to `main` (minor = feature, patch = fix/docs). Version in `--version`, `doctor`, the home title, `make install`. `CHANGELOG.md` Keep a Changelog.

### Home screen and ctrl+x (D-027), Full Disk Access detection (D-026), model uninstall (D-025), orphan removal fix, v0.2
- Home loop in `cmd/rootout/tui.go`; ctrl+x/ctrl+c quit everywhere via `tui.ErrQuit`, esc/q end one screen.
- `internal/tcc`: FDA probe; `doctor` shows it; `plan.MarkAccess` tags protected `~/Library/Containers` entries. The denied path has only run in unit tests.
- `ollama.Client.Delete`, picker `d`, `rootout config models rm`.
- `internal/trash` retries a failed batch one path at a time (macOS protects entries directly under `~/Library/Containers`; FDA for the terminal is the remedy).
- v0.2: progress reporter, typed `delete`, `rootout guide`, `rootout orphans`, scan 31 s → 5 s.

## Real-machine results (M1, 8 GB, 47 user apps, 12 VS Code extensions; not re-run this session)
- `rootout remove Spotify -n`: bundle + 7 leftovers, 3.8 GB; `Mail` refused.
- `rootout scan --all`: scores 5–30; the 30 is `ms-python.vscode-python-envs` piping the uv installer into `sh` (true finding).
- Live removal and live LLM calls have not been run.

## Blockers and open questions
- Unsigned binary relies on the cask's quarantine-clearing step (D-030); a future macOS may block it.
- `TAP_GITHUB_TOKEN` expires 2026-10-22 + 1 year; rotation in `docs/kb/release.md`. Users need no token.
- Intel Macs: `ollama.Recommend` caps them at the 8 GB tier, wrong for a large-RAM Intel Mac.
- `Group Containers` not probed for the TCC protection seen under `~/Library/Containers`.
- Binary is 26 MB stripped; the official SDKs (D-013) are a few MB of that.
- `sfltool dumpbtm` blocks without Full Disk Access; the login items probe has a 3 s timeout.
- The VSIX signature file name is unverified; `unsigned_gallery` is Low for that reason.

## How to verify
```bash
make build && make test && make lint && scripts/toolchain_check.sh
make release-check NEXT=v0.5.0 && make snapshot
grep -c download_strategy dist/homebrew/Casks/rootout.rb   # 0
ruby -c dist/homebrew/Casks/rootout.rb
./bin/rootout doctor
./bin/rootout remove Spotify --dry-run
./bin/rootout scan --all --no-tui
```

## Gotchas
- Driving the TUI from a script: fork a pty, wait for a screen string, then send keys (keys sent before the alternate screen opens are eaten; `ESC` arriving with the next byte parses as Alt). A throwaway `drive.py` did this for the setup screen.
- `goreleaser check` on 2.19 rejects the old `hooks.post.install` syntax, so `make release-check` was red on `main` before this PR; `archives.files` hard-fails when LICENSE is missing.
- The snapshot cask names the last tag in its URL (`v0.4.1` → 404 if installed by hand); only a real release writes a working cask.
- Scan speed: never run a regex over a whole bundle; use `FileCtx.FindAround` (docs/kb/heuristics.md, Performance).
- ctrl+x/ctrl+c must be checked before the `?` overlay close and before delegating to the typed box / embedded picker.
- pty testing: wait for the alternate screen before sending keys.
- The tool was renamed from `purge` to `rootout` (D-018) because macOS ships `/usr/sbin/purge`. `make install` puts it in `~/.local/bin`, which hides the brew copy.
- A failing `go test` piped into `tail` hides the exit code; check `${PIPESTATUS[0]}` or grep for FAIL.
- Charm v2: `charm.land/...` imports; lipgloss sizes include borders.
- zsh expands a bare `====` argument (`echo ====` fails with "=== not found"); quote separators in shell one-liners.
- `go test ./... | tail` hides a red exit code; redirect to a file and check `$?` (bit me once this session: a commit went in with a failing test and had to be amended).
- `/usr/bin/trash` rejects `--`; Homebrew's `trash` is a different tool.
- macOS has no `timeout(1)`: `perl -e 'alarm N; exec @ARGV' -- cmd`.
- gosec G101 flags constant names containing "Assess"/"Key"-like words; rename rather than suppress.

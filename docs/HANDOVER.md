# Handover

_Updated: 2026-11-04 · Session: scan/LLM enhancements in four phases (D-035…D-038) on four stacked branches_

## Status
**Four enhancements are being shipped as four stacked branches/PRs** (plan: `~/.claude/plans/declarative-frolicking-patterson.md`, summarised here): (1) hosted model choice, (2) `l` assesses the ticked rows only with a bounded queue and a multi-result pane, (3) ✓/✗/⚠ verdict icons in the list, (4) assessments from paid providers persisted across runs. Decisions taken with Shivang: ticked rows are the uninstall tick and `l` refuses with a hint when nothing is ticked; icons come from the LLM verdict only (threshold 70 %); model choice in the wizard plus `m` and `config models`, no one-time prompt; one PR per phase, later branches stack on the earlier one until Shivang merges.

**All four phases are code-complete, each on its own branch stacked on the previous one** (`m7/hosted-model` → `m7/assess-ticked` → `m7/verdict-icons` → `m7/assess-cache`), `make test`/`make lint` green at every tip, one DECISIONS entry each (D-035…D-038), CHANGELOG "Unreleased" carries all four, `docs/kb/assessments.md` is the kb page.

- **Phase 1 (D-035, v0.7.0)**: `anthropic/openai.ListModels` (`Verify` wraps them), `providers.ListModels`, `tui.HostedPicker` + `embeddedPicker`, the wizard's model step after a verified key, `m` on the scan screen for hosted providers, `config models --provider anthropic|openai [--pick]`, generic `saveModel`, home AI line refreshed after a scan. Live: `config models --provider anthropic` listed the real catalogue with the stored key.
- **Phase 2 (D-036, v0.8.0)**: `l` asks about the ticked rows only, two at a time (`assessWorkers`), skips answered rows, retries errors, hint when nothing is ticked; "AI summary" pane once more than one row is ticked and one asked.
- **Phase 3 (D-037, v0.9.0)**: ✓/✗/⚠/! after the editor tag from `llm.Assessment` only (`llm.ConfidenceThreshold` 0.70, `Confident()`); legend on the AI line and in `?`; rows too narrow for the editor tag drop it.
- **Phase 4 (D-038, v0.10.0)**: `internal/assesscache` (JSON under `~/.cache/rootout`, `ROOTOUT_CACHE`), `llm.PromptVersion` + `Fingerprint` with a pinning test, cache wired in `cmd/rootout/assess.go` only, preload + `L` on the scan screen, `scan --no-cache`, `config cache clear`, `config show` line, "saved"/`"cached": true` markers. Live on this Mac: `scan --no-tui --llm --min-score 30` asked Claude about 9 extensions; the second run answered all 9 from the file; `--json` shows 9 cached rows.

Driven through a pty against a fake local model: tick two rows → `l` → "asking 2" → summary pane with both verdicts, ✓ icons and the legend, `?` lists `L` and the icon key. Not yet run in a real terminal: the wizard's model step and `m` → hosted picker (view tests cover them). Two fresh reviewers read the branches; their findings are fixed in the last commit of `m7/hosted-model` (stale cancelled-fetch message, Responses-incompatible OpenAI ids, blank-Keychain panic, `--pick` after a failed fetch) and of `m7/assess-cache` (`config cache clear` refuses anything that is not a rootout cache file, ⚠ with U+FE0E, unticked queued rows are not sent, one cache instance per run, footer fits 80 columns). Deferred minors are listed in the session's final message and below.

**Next**: push the four branches, open PR 1 against `main` and PRs 2–4 each against the previous branch (retarget to `main` as Shivang merges), tag v0.7.0 … v0.10.0 in order after each merge (`make release-check NEXT=…`).

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
1. Manual on this Mac: `rootout setup` → Claude → enter (keeps the stored key, live check) → model list → choose → home shows `AI: Claude · <model>`; scan → `m` → hosted picker; then the Ollama path with an installed model; then a scan with `l`.
2. `~/.local/bin/rootout` (an old `make install` build) is still on this Mac and may shadow the brew copy: `rm ~/.local/bin/rootout` (`docs/kb/release.md`, "Which binary am I running").
3. Earlier manual checks still open: model grid and home loop in a real terminal; Full Disk Access denied path; v0.2 interactive flows; M2 QA on a throwaway account; live `scan --llm`.
4. Optional: signing and notarisation (D-030 / D-015 quill flow); homebrew-cask submission once the project meets Homebrew's notability bar.
5. One-time history scan: `brew install gitleaks && gitleaks git .`.
6. Dependabot PRs #3–#5 (anthropic-sdk-go, openai-go, bubbletea) are open; review, merge and tag patch releases.

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

# Handover

_Updated: 2026-11-03 · Session: open-source release (D-033) shipped as v0.5.0: MIT, public tap, token-free install; membership gate dropped_

## Status
**v0.5.0 is released and verified.** rootout is open source under MIT, the tap `shiva2021/homebrew-rootout` is public, and the cask is a plain download: in a shell with no `HOMEBREW_GITHUB_API_TOKEN`, `brew tap shiva2021/rootout && brew trust shiva2021/rootout && brew install --cask rootout` installed `/opt/homebrew/bin/rootout` v0.5.0 with no quarantine attribute, and `doctor` runs (2026-11-03). PR #2 merged, Release workflow green, release has two archives + `checksums.txt` + CHANGELOG notes, tap cask has the plain URL and `postflight_steps`, tap README carries the install steps, GitHub shows "MIT License", private vulnerability reporting is on. The membership gate is **dropped for good**: PR #1 (`m6/membership`) is closed, branch kept as an archive; its `service/` leftovers are in `../rootout-m6-service.bak`, outside the repo.

## Done this session (2026-11-03)
- Shipped v0.5.0 and verified the token-free install (see Status). The tap went public only after the release had rewritten its cask.
- Found the repo already public with no licence; the handover on `main` (2026-10-22) and the branch handover (2026-10-30) were both written for a private repo with a paywall. Shivang chose: keep public, MIT, drop the gate, park the branch, scrub the LeetCode login at HEAD only.
- D-033 logged (supersedes D-008, D-024, D-031). The parked branch has its own D-033/D-034 drafts; `main` takes the number.
- `.goreleaser.yaml`: no `url`/`custom_block`; GoReleaser's default release URL; `install_steps` hook; LICENSE in archives; new caveats. Workflow pin `v2.19.0`. Verified: `goreleaser check`, snapshot cask passes `ruby -c`, `actionlint` clean.
- `internal/license` deleted with its call site and `exitLicensing`; exit codes are 0–3 (`docs/kb/conventions.md` unchanged, it never listed 4).
- `docs/research/vscode-extension-audit/REPORT.md:47`: login replaced by "redacted". It stays in history since `90ba3fb` (Shivang's call: no rewrite).
- `.gitignore`: `/service/`, `/.idea/`. Repo topics and homepage set on GitHub.

## Next steps
1. `~/.local/bin/rootout` (a `make install` build, `v0.4.1-3-gb7dbb2e`) is still on this Mac and may shadow the brew copy depending on `PATH` order: `rm ~/.local/bin/rootout` (`docs/kb/release.md`, "Which binary am I running").
2. Earlier manual checks still open: model grid and home loop in a real terminal; Full Disk Access denied path; v0.2 interactive flows; M2 QA on a throwaway account; live `scan --llm`.
3. Optional: signing and notarisation (D-030 / D-015 quill flow); homebrew-cask submission once the project meets Homebrew's notability bar (own tap until then).
4. One-time history scan: `brew install gitleaks && gitleaks git .` (grep for token patterns found only test values).
5. Dependabot will open its first PRs for Go modules and Actions; review and tag patch releases as they merge.

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
- `goreleaser check` on 2.19 rejects the old `hooks.post.install` syntax, so `make release-check` was red on `main` before this PR; `archives.files` hard-fails when LICENSE is missing.
- The snapshot cask names the last tag in its URL (`v0.4.1` → 404 if installed by hand); only a real release writes a working cask.
- Scan speed: never run a regex over a whole bundle; use `FileCtx.FindAround` (docs/kb/heuristics.md, Performance).
- ctrl+x/ctrl+c must be checked before the `?` overlay close and before delegating to the typed box / embedded picker.
- pty testing: wait for the alternate screen before sending keys.
- The tool was renamed from `purge` to `rootout` (D-018) because macOS ships `/usr/sbin/purge`. `make install` puts it in `~/.local/bin`, which hides the brew copy.
- A failing `go test` piped into `tail` hides the exit code; check `${PIPESTATUS[0]}` or grep for FAIL.
- Charm v2: `charm.land/...` imports; lipgloss sizes include borders.
- `/usr/bin/trash` rejects `--`; Homebrew's `trash` is a different tool.
- macOS has no `timeout(1)`: `perl -e 'alarm N; exec @ARGV' -- cmd`.
- gosec G101 flags constant names containing "Assess"/"Key"-like words; rename rather than suppress.

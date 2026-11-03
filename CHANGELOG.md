# Changelog

All notable changes to rootout. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [SemVer](https://semver.org/). Tags are cut on every merge to `main` (D-028).

## [Unreleased]

### Added
- Choose the Claude or OpenAI model you pay for: the setup screen lists the models your key can use right after the key check (the check is the same request) and saves the choice; `m` in the scan screen and `rootout config models --provider anthropic|openai [--pick]` change it later. Type an id by hand when the list cannot be fetched (D-035).

- Scan list: a green ✓ (benign), red ✗ (malicious) or yellow ⚠ (suspicious, unknown, or under 70 % confidence) after each assessed extension, `!` when the model call failed, with a legend on the AI line and in `?` (D-037). Text output (`--no-tui`) is unchanged.

### Changed
- `rootout config models` gained `--provider`; the home screen refreshes its AI line after a scan, since `m` can change the saved model.
- Scan screen: `l` now asks the model about every ticked extension (the same tick as uninstall), two at a time, instead of the cursor row; with nothing ticked it shows a hint and sends nothing. Rows already answered are skipped and failed ones retried; re-asking an answered row arrives with the saved-answers feature. With several ticked and at least one asked, the right pane lists every ticked extension with its verdict, confidence and first sentence of reasoning (D-036).

## [0.6.1] - 2026-11-03

### Changed
- Docs: handover and release runbook record the v0.6.0 release, the stale-tap `brew update` step and the `main`-only ruleset; no code changes.

## [0.6.0] - 2026-11-03

### Added
- First-launch setup screen and `rootout setup`: choose a local Ollama model, paste a Claude or OpenAI key (checked with one free request, then stored in the Keychain; retry, go back or save anyway on failure), or skip. AI stays optional (D-034).
- The home screen shows a one-line AI status under the title.

### Changed
- The home row "Choose local AI model" is now "Set up AI" and opens the setup screen; the local model picker stays available from `rootout config models --pick` and `m` in the scan screen.
- The first-run hint on subcommands names `rootout setup`; the scan screen's "AI: none" line mentions it.

## [0.5.1] - 2026-11-03

### Changed
- Docs: handover records the verified v0.5.0 release and the public tap; no code changes.

## [0.5.0] - 2026-11-03

### Added
- MIT licence; rootout is open source (D-033).
- Token-free install from the public Homebrew tap: `brew tap shiva2021/rootout && brew trust shiva2021/rootout && brew install --cask rootout`.
- `SECURITY.md` and Dependabot updates for Go modules and GitHub Actions.

### Changed
- GoReleaser 2.19.0; the cask clears quarantine with `postflight_steps` instead of the deprecated `postflight`.
- Release archives include `LICENSE`.

### Removed
- The private-tap download strategy and the `HOMEBREW_GITHUB_API_TOKEN` requirement.
- The no-op licence seam and exit code 4; the planned membership gate (D-024) is dropped.

## [0.4.1] - 2026-10-22

### Fixed
- Homebrew tap: v0.4.0 was published to GitHub Releases without its cask because the tap token could not reach `shiva2021/homebrew-rootout`; this release writes the cask.
- Release notes: v0.4.0's GitHub Release body was empty because `changelog.disable` also skipped loading `--release-notes`; the setting is removed.

## [0.4.0] - 2026-10-22

### Added
- Release pipeline: GoReleaser builds darwin arm64/amd64 on every v* tag, GitHub Release notes come from this file, and a private Homebrew tap (shiva2021/homebrew-rootout) is updated (D-030…D-032). `make release-check` and `make snapshot`.

## [0.3.1] - 2026-10-22

### Changed
- Model picker and `rootout config models` lay models out in one column grid (MODEL · SIZE · FIT · NOTES) with a header row; models too large for this Mac are grouped under "Need more memory than this Mac has"; in the picker the selected model's description sits on its own line above the footer (D-029).

## [0.3.0] - 2026-10-21

### Added
- Home screen: bare `rootout` lists Uninstall an app · Remove orphaned leftovers · Scan editor extensions · Choose local AI model · Quit, and returns there after every action with a one-line outcome (D-027).
- Full Disk Access detection: `doctor` reports it; removal plans tag paths that need it and warn instead of blocking (D-026).
- Uninstall local Ollama models from the picker (`d`) and `rootout config models rm <model>` (D-025).
- Scan progress bar; Ollama pull retries and format fallback.
- `rootout --version`, a `rootout <version>` line at the top of `doctor`, the version in the home-screen title, and `make install` echoing what it installed (D-028).

### Changed
- `ctrl+x` (and `ctrl+c`) quit from every screen; `esc` now goes back one screen instead of quitting. Subcommands treat ctrl+x like esc (same exit codes).
- The app picker's pinned "Orphaned leftovers…" row moved to the home screen.
- `rootout scan --uninstall <id>` no longer scores the extensions or does `--online` lookups first (that work was never used on that path).

### Fixed
- Trash moves: a failed batch is retried one path at a time, so one protected path no longer sinks the whole batch; access-denied errors hint at Full Disk Access.

## [0.2.0] - 2026-10-20 (untagged)
Progress reporting everywhere; typed `delete` confirmation; `rootout guide`, `?` overlays; `AI:` header and in-screen model picker on the scan screen; `rootout orphans`; scan performance (31 s → 5 s).

## [0.1.0] - 2026-10-19 (untagged)
First working version: `rootout` picker → review → remove, `list`, `remove` (Trash/permanent/dry-run, brew zap, admin elevation, receipts, launchd bootout), `scan` for five VS Code-family editors with heuristics and optional Claude/OpenAI/Ollama assessment, `config`, `doctor`.

[Unreleased]: https://github.com/shiva2021/rootout/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/shiva2021/rootout/releases/tag/v0.3.0

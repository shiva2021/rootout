# Changelog

All notable changes to rootout. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [SemVer](https://semver.org/). Tags are cut on every merge to `main` (D-028).

## [Unreleased]

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

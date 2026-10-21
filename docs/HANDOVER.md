# Handover

_Updated: 2026-10-21 · Session: v0.2 (progress, typed delete, help, in-scan model pick, orphans)_

## Status
v0.2 complete and pushed to https://github.com/shiva2021/rootout (`main`): progress bars, typed `delete`, help (examples, `rootout guide`, `?` overlays), in-scan local-model picking, `rootout orphans`. Decisions D-019…D-023. `make test`/`make lint` green. Remaining: M2 manual QA on a throwaway account, M5 release pipeline, M6 licensing (own plan).

## Done this session (v0.2)
- Progress: `internal/progress`; hooks in app discovery (4 size workers), leftover search, extension scan, removal; picker loads inside the program.
- Typed `delete` in the confirm and scan screens and the plain prompt; `--yes` is the only bypass; `scan --uninstall <id> [--yes]`.
- Help: `Long`/`Example` everywhere, `rootout guide`, first-run hint, `?` overlays.
- Scan screen: `AI:` header, `m` embeds the model picker, `scan --pick-model`.
- `rootout orphans`: `leftover.FindOrphans` (reverse-DNS ids only; owned via installed apps, Apple, SDK list, Spotlight, launchd, processes; vendor-still-installed annotation). On this Mac: 29 candidates, all plausible (old Telegram, ChatGPT, 1Password 7, RStudio, DaVinci…); nothing under com.apple.
- Scan performance: 31 s → 5 s on this Mac (`FindAround`, literal gates, no counted repetitions).

## Done earlier
- Plan (`docs/PLAN.md`), decisions D-001…D-017, knowledge base.
- Apps: `rootout` (picker → review → remove, loops), `list`, `remove` (Trash/permanent/dry-run, brew zap, one admin elevation, receipts, launchd bootout).
- Extensions: `scan` (5 editors, 4 rule groups, capability/intent scoring, `--online` registry data, review screen with uninstall), golden corpus.
- AI: Anthropic and OpenAI via official SDKs with structured JSON, Ollama via HTTP (`think:false`); keys in Keychain; `config` command; Ollama picker that detects, installs, starts, recommends by RAM tier and pulls with progress.
- `doctor`.

## Real-machine results (this Mac: M1, 8 GB, 47 user apps, 12 VS Code extensions)
- `rootout remove Spotify -n`: bundle + 7 leftovers, 3.8 GB; `Mail` refused.
- `rootout scan --all`: scores 5–30. The 30 is `ms-python.vscode-python-envs`, which pipes the official uv installer from astral.sh into `sh` (true finding, Medium).
- Review screen and app picker verified under a pseudo-terminal (render + quit). Live removal and live LLM calls have not been run.

## Next steps
1. Try v0.2 interactively: `rootout` (bar, orphans row, `?`), `rootout scan` (`m`), `rootout orphans` (tick one group, type delete) — removal paths have only run in unit tests plus Esc-at-the-box pty checks.
2. M2 task 7: follow `docs/kb/qa.md` on a throwaway macOS user account (cask, App Store and pkg app). Live removal has only run in unit tests.
3. Try `rootout scan --llm` with a real key or `rootout config models --pick` with Ollama; only recorded-HTTP tests exist so far.
4. M5: GoReleaser config, Homebrew tap; notarization optional (D-024). It is a CLI release, not a macOS app release.
5. M6 membership gate: write a separate plan first. Admin (Shivang) unrestricted via an admin credential; everyone else needs an active Patreon monthly membership (mechanics explored at M6). See D-024.

## Blockers and open questions
- `docs/research/vscode-extension-audit/` names Shivang's LeetCode username; remove it before the repo ever goes public.
- Binary is 26 MB stripped (was 9 MB before the AI wiring). The official SDKs (D-013) are a few MB of that; replacing them with plain HTTP would shrink it but reverses D-013. Shivang to decide.
- `sfltool dumpbtm` blocks without Full Disk Access; login items probe has a 3 s timeout and is otherwise skipped.
- The VSIX signature file name is unverified; `unsigned_gallery` is Low for that reason (all 12 local extensions have `.vsixmanifest` but none of the guessed signature files).

## How to verify
```bash
make build && make test && make lint && scripts/toolchain_check.sh
./bin/rootout doctor
./bin/rootout remove Spotify --dry-run
./bin/rootout scan --all --no-tui
```

## Gotchas
- Scan speed: never run a regex over a whole bundle; use `FileCtx.FindAround` with the pattern's mandatory literals and avoid `{200,}`-style repetitions (docs/kb/heuristics.md, Performance). Profile with the throwaway timing test described there.
- pty testing: keys sent before the alternate screen opens are buffered and `ESC ESC q` then parses as Alt-combinations; wait for the screen before sending keys.
- The tool was renamed from `purge` to `rootout` (D-018) because macOS ships `/usr/sbin/purge`. `make install` puts it in `~/.local/bin`.
- A failing `go test` piped into `tail` hides the exit code; one bad commit slipped through that way (fixed in the next commit). Check `${PIPESTATUS[0]}` or grep for FAIL.
- Charm v2: `charm.land/...` imports, alt screen via `view.AltScreen`, lipgloss sizes include borders.
- `/usr/bin/trash` rejects `--`; Homebrew's `trash` is a different tool.
- Minified bundles are one line: any rule pairing two patterns needs a byte window (see `docs/kb/heuristics.md`).
- macOS has no `timeout(1)`: `perl -e 'alarm N; exec @ARGV' -- cmd`.
- gosec G101 flags constant names containing "Assess"/"Key"-like words; rename rather than suppress.

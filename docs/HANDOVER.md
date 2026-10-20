# Handover

_Updated: 2026-10-20 · Session: planning + M0–M4_

## Status
M0–M4 code complete; `make test` and `make lint` green; 28 commits on `main`, local only (no GitHub remote yet). Remaining: M2 manual QA on a throwaway account, M5 release pipeline, M6 licensing (needs its own plan).

## Done this session
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
1. M2 task 7: follow `docs/kb/qa.md` on a throwaway macOS user account (cask, App Store and pkg app). Live removal has only run in unit tests.
2. Try `rootout scan --llm` with a real key or `rootout config models --pick` with Ollama; only recorded-HTTP tests exist so far.
3. Create the private GitHub repo and push (needs Shivang's go-ahead).
4. M5: GoReleaser config, notarization (needs Apple Developer account), private Homebrew tap.
5. M6 licensing: write a separate plan first.

## Blockers and open questions
- GitHub push: waiting for explicit go-ahead.
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
- The tool was renamed from `purge` to `rootout` (D-018) because macOS ships `/usr/sbin/purge`. `make install` puts it in `~/.local/bin`.
- A failing `go test` piped into `tail` hides the exit code; one bad commit slipped through that way (fixed in the next commit). Check `${PIPESTATUS[0]}` or grep for FAIL.
- Charm v2: `charm.land/...` imports, alt screen via `view.AltScreen`, lipgloss sizes include borders.
- `/usr/bin/trash` rejects `--`; Homebrew's `trash` is a different tool.
- Minified bundles are one line: any rule pairing two patterns needs a byte window (see `docs/kb/heuristics.md`).
- macOS has no `timeout(1)`: `perl -e 'alarm N; exec @ARGV' -- cmd`.
- gosec G101 flags constant names containing "Assess"/"Key"-like words; rename rather than suppress.

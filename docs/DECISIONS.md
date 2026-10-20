# Decisions

Append-only. Newest at the bottom. Never edit an old entry; add a new one that supersedes it.

## D-001 · 2026-10-20 · Language: Go, cgo-free static binary

**Context.** A lightweight macOS CLI that end users install; later a paid product. Must start fast, ship as one file, and be easy to notarize.
**Decision.** Go, built with `CGO_ENABLED=0`, pinned via `go`/`toolchain` directives in `go.mod`.
**Alternatives.** TypeScript/Node (needs a runtime or a ~50 MB bundle), Swift (native but thin TUI/LLM ecosystem), Rust (same shipping story, slower iteration).
**Consequences.** Anything needing Apple frameworks (Keychain, Trash) is done through system CLIs, not cgo.
**Status.** Accepted

## D-002 · 2026-10-20 · Interactive TUI plus scriptable subcommands

**Context.** The brief asks for a list-and-search experience on launch, but a security tool also needs to be scriptable and testable from the shell.
**Decision.** Bare `purge` opens a Bubble Tea TUI. Cobra subcommands `list`, `remove`, `scan`, `config`, `doctor` cover the same actions non-interactively, each with `--json`. TUI and subcommands share one core; only confirmation UI and rendering differ.
**Alternatives.** TUI only (not scriptable), subcommands only (not the asked-for experience).
**Consequences.** A `Confirmer` interface abstracts how confirmation is collected (TUI, y/N prompt, `--yes`).
**Status.** Accepted

## D-003 · 2026-10-20 · Move to Trash by default

**Context.** Leftover matching is heuristic; a wrong match must be recoverable.
**Decision.** All removed paths go to the macOS Trash. `--permanent` deletes outright, `--dry-run` only lists, and every removal shows the full file list and asks for confirmation unless `--yes`.
**Alternatives.** Permanent delete by default (irreversible), Trash-only with no permanent option (loses the one-step use case).
**Consequences.** Disk space is freed only when the user empties Trash; documented in the confirm screen.
**Status.** Accepted

## D-004 · 2026-10-20 · AI providers v1: Claude, OpenAI, Ollama behind one interface

**Context.** Threat assessment should work with hosted models via API key or a local model.
**Decision.** One `llm.Provider` interface with three adapters. The tool is fully usable with no key configured; heuristics never depend on an LLM.
**Alternatives.** Claude only; a single generic OpenAI-compatible adapter.
**Consequences.** Three thin adapters to keep current; a fake provider for tests.
**Status.** Accepted

## D-005 · 2026-10-20 · Scan VS Code and its forks

**Context.** Cursor, VSCodium, Windsurf and VS Code Insiders use the same extension directory layout.
**Decision.** Scan all five editors.
**Alternatives.** VS Code only.
**Consequences.** Extension directory resolution must handle each editor (see D-016); registry lookups route to Open VSX for the forks (see D-014).
**Status.** Accepted

## D-006 · 2026-10-20 · API keys in macOS Keychain

**Context.** A security tool should not keep plaintext secrets on disk.
**Decision.** Keys live in the login Keychain; environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) override. The config file never stores secrets.
**Alternatives.** `~/.config/purge/config.toml` with 0600 permissions.
**Consequences.** Keychain access may prompt the user once; documented in setup.
**Status.** Accepted

## D-007 · 2026-10-20 · Repo at ~/Projects/purge, private on GitHub, binary named `purge`

**Context.** The project becomes a paid product.
**Decision.** Module `github.com/shiva2021/purge`, private repository, command name `purge`.
**Alternatives.** Public/open-core repo; local-only; names `macsweep`, `uninstallr`, `cleanslate`.
**Consequences.** Homebrew distribution uses a private tap.
**Status.** Accepted

## D-008 · 2026-10-20 · Licensing is a no-op seam until the last milestone

**Context.** Subscription is required eventually but must not shape early work.
**Decision.** `internal/license.Check(ctx)` always returns Allowed and is called once from the Cobra root. Real licensing is milestone M6 with its own plan.
**Alternatives.** Build billing early.
**Consequences.** Gating points must route through `license.Check` from day one so M6 needs no restructuring.
**Status.** Accepted

## D-009 · 2026-10-20 · Ollama model picker with hardware-aware recommendations

**Context.** Choosing a local model should not require knowing Ollama's catalogue or what fits the machine.
**Decision.** Selecting Ollama lists installed models from `/api/tags`, recommends models from a built-in catalogue keyed by RAM tier and chip (probe via `sysctl`), and pulls the chosen model with streamed progress via `/api/pull`. If Ollama is missing, the CLI shows the official install routes and offers to run `brew install ollama` after confirmation.
**Alternatives.** Free-text model name only; silently pulling a default.
**Consequences.** The catalogue is data in `catalog.go`; model names must be verified against ollama.com/library when M4 starts.
**Status.** Accepted

## D-010 · 2026-10-20 · Charm v2 stack, Cobra, sahilm/fuzzy, howett.net/plist

**Context.** Bubble Tea v2 moved to `charm.land/...` import paths and has been stable since 2026-02.
**Decision.** `charm.land/bubbletea/v2` 2.1.0, `bubbles/v2` 2.2.1, `lipgloss/v2` 2.0.6, `huh/v2` 2.0.3; Cobra 1.10.2; `sahilm/fuzzy` 0.1.3; `howett.net/plist` for Info.plist parsing.
**Alternatives.** Bubble Tea v1 (`github.com/charmbracelet/...`, last release 2025-09); urfave/cli v3.
**Consequences.** Never mix v1 and v2 Charm packages.
**Status.** Accepted

## D-011 · 2026-10-20 · Trash via /usr/bin/trash with Finder fallback; minimum macOS 14

**Context.** `/usr/bin/trash` first appeared in macOS 15; `NSFileManager` would need cgo.
**Decision.** Exec `/usr/bin/trash` when present; on macOS 14 fall back to `osascript` telling Finder to delete the file. Support macOS 14 and later.
**Alternatives.** Rename into `~/.Trash` (no Put Back, fails across volumes); cgo `trashItemAtURL`.
**Consequences.** The Finder fallback triggers an Automation permission prompt on first use.
**Status.** Accepted

## D-012 · 2026-10-20 · Keychain via zalando/go-keyring

**Context.** Need Keychain access without cgo.
**Decision.** `github.com/zalando/go-keyring` 0.2.8, which shells out to `/usr/bin/security`.
**Alternatives.** `keybase/go-keychain` (cgo; the binary becomes the ACL principal, so an unstable codesign identity breaks access).
**Consequences.** Reads may show a Keychain "allow" prompt because `security` is the caller.
**Status.** Accepted

## D-013 · 2026-10-20 · Official SDKs for Anthropic and OpenAI, plain HTTP for Ollama

**Context.** Hosted provider APIs change quickly; Ollama's API is small and its Go package drags in the whole server module.
**Decision.** `anthropic-sdk-go` 1.82.0 using GA structured outputs (`output_config`), `openai-go/v3` 3.82.0 using the Responses API with a strict JSON schema, and stdlib `net/http` against Ollama's `/api/chat` with a JSON schema in `format`. No forced tool-choice (returns 400 on current Claude models).
**Alternatives.** Raw HTTP for all three; Ollama's OpenAI-compatible endpoint.
**Consequences.** Two SDK dependencies to keep updated; Ollama adapter is ~100 lines we own.
**Status.** Accepted

## D-014 · 2026-10-20 · Registry enrichment is optional and off by default

**Context.** Verified-publisher status and install counts are useful signals but require network; the tool must work offline.
**Decision.** `--online` enables lookups: VS Code via the Marketplace `extensionquery` API, Cursor/Windsurf/VSCodium via the Open VSX REST API. Heuristic scores never depend on these.
**Alternatives.** Always online; never online.
**Consequences.** `internal/registry` is isolated so API changes cannot break scanning.
**Status.** Accepted

## D-015 · 2026-10-20 · GoReleaser with homebrew_casks and quill notarization

**Context.** GoReleaser's `brews` section is hard-deprecated since 2.16; native codesign/notarize is Pro-only.
**Decision.** GoReleaser 2.18.3, `homebrew_casks` for the private tap, `notarize.macos` (quill) with a Developer ID .p12 and App Store Connect API key.
**Alternatives.** Manual `codesign`/`notarytool` scripts; GoReleaser Pro.
**Consequences.** Needs a paid Apple Developer account before M5.
**Status.** Accepted

## D-016 · 2026-10-20 · Resolve extension directories from each editor's product.json

**Context.** Only VS Code's `~/.vscode/extensions` is officially documented; the forks' paths come from community sources.
**Decision.** Read `dataFolderName` from each installed editor's `Contents/Resources/app/product.json`; fall back to `~/.vscode`, `~/.vscode-insiders`, `~/.cursor`, `~/.vscode-oss`, `~/.windsurf`.
**Alternatives.** Hard-coded list only.
**Consequences.** Works for renamed or portable installs; adds one plist-free JSON read per editor.
**Status.** Accepted

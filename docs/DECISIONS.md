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
**Status.** Superseded by D-018

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

## D-017 · 2026-10-20 · Score capability and intent separately

**Context.** The first real scan of this Mac's 12 VS Code extensions gave 9 of them a score of 100, including Microsoft's Python and Jupyter extensions. Two causes: rules matching unrelated code in minified single-line bundles, and a scoring model where ordinary capabilities (`eval`, spawning processes, minification) each added up to 60 points.
**Decision.** Rules are split into capability rules (eval, child processes, obfuscation, environment reads, activation, staleness, unsigned) and intent indicators (hidden payloads, credential access, exfiltration hosts, download-and-run chains, typosquats, permission bypass). Capability findings together add at most 35 points (15 for publishers in `heur.TrustedPublishers`). Indicators contribute their top severity weight, plus up to 50% for repeats, and combine as 1 - Π(1 - p). Indicators are never discounted for trusted publishers, because a hijacked publisher is the supply-chain case. Rules that pair two patterns now require them within a short byte window, not just in the same file. npm lifecycle scripts drop to Low because VS Code does not run them on install; `vscode:uninstall` is flagged instead. Advisories carry one fixed version per release line.
**Alternatives.** A flat per-rule cap (saturates on any large bundle); discounting everything for trusted publishers (hides a compromised publisher); allowlisting known extensions by id (unmaintainable and blind to hijacked updates).
**Consequences.** `internal/heur/score.go` holds the capability list; a new rule is an indicator unless added there. `internal/heur/corpus_test.go` pins both directions: benign samples stay under 30, malicious samples clear 50. On this Mac the 12 real extensions now score 5 to 30; the 30 is `ms-python.vscode-python-envs`, which pipes the official uv installer from astral.sh into `sh`.
**Status.** Accepted

## D-018 · 2026-10-20 · Rename the tool from `purge` to `rootout`

**Context.** macOS ships `/usr/sbin/purge` (flushes the disk cache, needs root). Running `purge` before the tool was installed hit Apple's command ("Unable to purge disk buffers: Operation not permitted"). Even once installed, the name would depend on PATH order, and `sudo purge` would always run Apple's tool.
**Decision.** Command, module (`github.com/shiva2021/rootout`), repo folder (`~/Projects/rootout`), config dir (`~/.config/rootout`), env var (`ROOTOUT_CONFIG`) and Keychain service (`rootout`) all renamed. Nothing had been stored under the old names, so there is no migration.
**Alternatives.** Keep `purge` and rely on Homebrew's PATH order (fragile); `macsweep` (free as a command, but about 30 GitHub projects use it, almost all Mac cleaners); `appgone`, `leavenone`, `traceout` (also clean). 32 names were checked for a macOS command, a Homebrew formula or cask, and same-category GitHub projects; `rootout` had none of the first two and 4 unrelated GitHub repos.
**Consequences.** Trademark and domain availability are not yet checked; do that before a paid release.
**Status.** Accepted

## D-019 · 2026-10-20 · Interactive uninstalls require typing `delete`

**Context.** Shivang asked that nothing be uninstalled without the user typing `delete`. The flows were confirmed with Enter or `y`.
**Decision.** The TUI confirm screen, the scan review screen and the plain prompt all require the exact word `delete` (case-insensitive, trimmed) for apps, orphans and extensions. `--yes` remains the scripting bypass and prints a warning banner; `scan` gains `--uninstall <id>` and `--yes` so extensions have the same scripted path.
**Alternatives.** No bypass at all (breaks scripting); typed word only for `--permanent`.
**Consequences.** One shared `typedConfirm` sub-model in `internal/tui` and `plan.IsConfirmWord`; every new destructive screen must use them.
**Status.** Accepted

## D-020 · 2026-10-20 · Orphan leftovers: conservative detection, nothing pre-selected

**Context.** Files left by apps uninstalled before rootout existed cannot be matched to an installed app, so the usual bundle-id matching does not apply and false positives would delete live data.
**Decision.** `rootout orphans` considers only entries in `~/Library` whose name parses as a reverse-DNS bundle id (never plain names). An id is owned, and therefore not an orphan, if any installed app has it or a prefix of it, if it is a prefix of an installed id (vendor-shared folders), if it is `com.apple.*`, if any app bundle anywhere on disk (Spotlight) carries it, or if a loaded launchd job or running process uses it. Results are grouped by owner id, nothing is ticked, groups changed within 30 days are marked "recently used", and removal needs the typed word. `/Library` and receipts are report-only in this version.
**Alternatives.** Pre-ticking confident candidates; a report-only first release; matching plain names.
**Consequences.** Detection lives in `package leftover` to reuse the rules table; `plan.New` builds a plan without an app. The owned-set sources are listed in `docs/kb/safety.md`.
**Status.** Accepted

## D-021 · 2026-10-20 · Help: examples, a guide command, in-screen key help

**Context.** Help was Cobra's default output with no examples or orientation.
**Decision.** Every command gets `Example` and a fuller `Long`; the root help gets a "Getting started" block; `rootout guide` prints a tour whose steps reflect this machine (reusing the doctor checks); a one-time first-run hint points at it; every TUI screen opens a key overlay on `?`.
**Alternatives.** Shell completions (deferred); a wizard that runs commands for the user.
**Consequences.** New commands must ship with `Example`; new screens must register their keys with the overlay.
**Status.** Accepted

## D-022 · 2026-10-20 · Pick the local model inside the scan screen

**Context.** The model picker existed only behind `config models --pick`; the scan showed nothing about which AI, if any, would answer.
**Decision.** The scan review header shows the active provider and model; `m` opens the model picker as a sub-model embedded in the scan screen (same Bubble Tea program, no nested program); `scan --pick-model` opens it before scanning. After a pick the command rebuilds the provider and saves it as default.
**Alternatives.** Suspending the program and running a second one; asking every time no provider is set.
**Consequences.** `tui.Assessor` replaces the bare assess function so the screen knows the label and can swap the provider.
**Status.** Accepted

## D-023 · 2026-10-20 · Progress reporting through an optional Reporter

**Context.** Discovery, scanning and removal ran silently for seconds.
**Decision.** `internal/progress.Reporter` (Start/Step/Done) is an optional parameter on long operations, defaulting to a no-op. The CLI renders one updating line on stderr, silent when stderr is not a terminal or under `--json`. The app picker loads apps inside the program and shows the same bar. Bundle size walks run on four workers.
**Alternatives.** Context-carried reporters (hidden coupling); per-package ad-hoc callbacks (already one for Ollama pulls, kept).
**Consequences.** Every new long operation accepts a Reporter; tests use `progress.Recorder`.
**Status.** Accepted

## D-024 · 2026-10-21 · Not a standard macOS app; admin unrestricted; public use behind a Patreon membership (last phase)

**Context.** Shivang clarified how rootout reaches people: it is not rolled out as a standard Apple/macOS application. He is the admin and creator and must be able to use every feature without restriction. Everyone else should be able to use the CLI once they pay for a monthly membership through Patreon (the mechanics will be explored when we get there). All of this is the last phase.
**Decision.** Distribution stays a plain CLI binary: GitHub Releases plus a Homebrew tap, no App Store, no app bundle, no installer; signing/notarizing the bare binary is optional and decided at M5. M6 becomes a membership gate: an admin credential makes `license.Check` return Allowed for everything, offline; for other users, access requires an active Patreon membership, verified by a mechanism chosen at M6 (Patreon API/OAuth versus membership-linked signed license keys). Which commands are gated, the offline grace period and the non-member message are M6 decisions. Nothing is built before M5 is done.
**Alternatives.** Mac App Store / in-app purchase (rejected: not a standard app rollout, and a CLI does not fit the store); Lemon Squeezy/Paddle license sales (the earlier M6 recommendation; superseded by Patreon membership, to be confirmed at M6); gating from day one (rejected: it would shape early development).
**Consequences.** `license.Check` keeps its single call site at the Cobra root (D-008) so M6 needs no restructuring. `docs/PLAN.md` M5/M6 updated. A Patreon integration adds a network dependency to the paid path; the admin path must never depend on the network.
**Status.** Accepted (supersedes the Lemon Squeezy/Paddle recommendation in D-008's M6 note)

## D-025 · 2026-10-21 · Uninstalling local models: y/n confirmation, default cleared

**Context.** The model picker could install models but not remove them, and recommended rows only flagged the installed ones. Shivang asked for an install status on every row and an easy way to uninstall.
**Decision.** `d` in the picker and `rootout config models rm <model>` delete through `DELETE /api/delete`. Confirmation is a y/n prompt, not the typed `delete` word used for app removal: a model is re-downloadable, and the picker's other prompts (install, download) are already y/n. `--yes` skips the prompt without the red banner for the same reason. If the uninstalled model is the configured default, the Ollama model setting is cleared and the user is told to pick another; the provider setting is left alone. Recommended rows always show `installed` or `not installed`.
**Alternatives.** Typed `delete` (rejected: heavier than the risk); leaving the default pointing at a missing model (rejected: scans would fail later with a less helpful error); picker-only without a CLI command (rejected: `config pull` has a CLI form, so should its inverse).
**Consequences.** `ModelDeps` gains `ClearDefault`, injected from `cmd/`, so the embedded scan-screen picker needs no changes. Deleting is the only y/n-confirmed destructive action in the tool; everything that touches user files stays on the typed word.
**Status.** Accepted


## D-026 · 2026-10-21 · Detect missing Full Disk Access up front; warn, do not block

**Context.** Entries directly under `~/Library/Containers` are protected by macOS privacy controls (TCC): the user, Finder, `/usr/bin/trash` and even root cannot move or delete them unless the calling terminal has Full Disk Access. Last session's fix made such a path fail alone with a hint, but the user only learned about it after the run. Shivang verified that granting Warp Full Disk Access makes the removal work.
**Decision.** A new `internal/tcc` package probes Full Disk Access by listing a folder macOS only exposes to apps with that permission (`~/Library/Safari`, then `~/Library/Mail`, then `~/Library/Application Support/com.apple.TCC`); a permission error means denied, success means granted, no such folder means unknown. `doctor` (and therefore `guide`) gets a "Full Disk Access" line naming the terminal app from `TERM_PROGRAM` and the settings path to fix it. After a plan is built, items that are direct children of `~/Library/Containers` are noted "needs Full Disk Access" when the probe says denied; both confirm views tag those rows and show one warning line with the count and the terminal to grant. The run still proceeds: those paths fail individually with the existing hint, everything else is removed.
**Alternatives.** Blocking the run until access is granted (rejected: unrelated paths would be held hostage by a settings change); an admin password prompt (rejected: root is refused too, see the 2026-10-21 handover); probing by attempting the delete (rejected: destructive); putting the probe in `fsx` (rejected: it is a privacy mechanism, not a path helper).
**Consequences.** `Plan` gains a `Warning` string carried into `--json`. `Group Containers` is not probed and not tagged until someone confirms the same protection applies there. The `writable()` check still classifies such paths as user paths; that is unchanged.
**Status.** Accepted

## D-027 · 2026-10-21 · Home screen; ctrl+x quits, esc goes back

**Context.** Bare `rootout` jumped into the app picker with orphans as a pinned row; finishing or cancelling an action ended the session, and esc quit everywhere, which was too easy to hit.
**Decision.** A home screen lists the four actions (uninstall, orphans, scan, model) plus a flash line with the last outcome; every action returns to it. ctrl+x (and ctrl+c) quit from any screen via `tui.ErrQuit`; esc/q end only the current screen and the caller decides what back means (review to picker, picker/scan/model to home). Subcommands map `ErrQuit` to the same outcome as esc: exit 3 for remove/orphans/config, 0 for scan.
**Alternatives.** More pinned rows in the picker (does not scale); one mega-model or nested programs (harder to test than one program per screen).
**Consequences.** `OrphansRow`/`Pick.Orphans` are gone. `runOrphans`/`runScan` lose their discovery halves to `findOrphans`/`scanReports`. ctrl+x during a model download cancels it and quits. `rootout scan --uninstall <id>` no longer scores the extensions or does `--online` lookups first: it only needs the extension list (that work was never used on that path), so its stderr progress lines are gone.
**Status.** Accepted

## D-028 · 2026-10-21 · Semver tags on every merge; version shown in --version, doctor and the home title

**Context.** The Makefile already injected `git describe` into `main.version`, but with no tags the binary printed a bare commit hash, so there was no way to tell whether the installed `rootout` was the latest.
**Decision.** Start at `v0.3.0` (the home-screen milestone; v0.1/v0.2 were informal). Every merge to `main` gets an annotated tag: minor for features, patch for fixes/docs, cut at session wrap-up and pushed with `main`. The version is visible in `rootout --version`, as the first `doctor` line, in the home-screen title and in `make install`'s last line. `CHANGELOG.md` records each tag.
**Alternatives.** Tag only at milestones (M2/M5/M6) — `--version` would show `v0.3.0-N-g<hash>` between them, which still measures distance but reads worse. A version baked into source — drifts from git and needs a bump commit per release.
**Consequences.** `tui.PickAction`/`NewHome` take the version string. Wrap-up gains a tagging step. A build from an untagged or dirty tree says so in its version string.
**Status.** Accepted

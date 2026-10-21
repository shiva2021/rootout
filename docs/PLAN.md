# Plan: `purge` — macOS app uninstaller + VS Code extension scanner (CLI)

## Context

Shivang wants a lightweight macOS CLI that (1) lists every installed app, lets him fuzzy-search one and removes it *completely* (bundle plus all leftover files), and (2) scans VS Code-family extensions for malicious behaviour, optionally asking an LLM (Claude, OpenAI or local Ollama, keys stored in Keychain) for a threat verdict, then uninstalls the bad ones. When the local route is chosen, the CLI shows the Ollama models already installed, recommends models that fit this Mac, and walks the user through installing one. It becomes a subscription product, but billing is the very last milestone; for now only a seam is left for it.

Prior art on this machine: `~/Desktop/docs/vscode-extension-audit/REPORT.md` (today's manual audit) already lists concrete indicators (orphaned extension folders, `.obsolete` markers, `state.vscdb` keys, plaintext credential files, stale bundled deps with CVEs, `onStartupFinished` activation, telemetry hosts, `bypassPermissions` launches). Those become heuristics.

### Decisions already taken with Shivang (go to `docs/DECISIONS.md` on approval)

| ID | Decision | Alternatives rejected |
|---|---|---|
| D-001 | Language: **Go**, cgo-free static binary | TypeScript/Node (needs runtime), Swift (small TUI/LLM ecosystem), Rust (slower iteration) |
| D-002 | Interface: bare `purge` opens a **Bubble Tea TUI**; Cobra subcommands `list`, `remove`, `scan`, `config`, `doctor`, all with `--json` | TUI-only (not scriptable), subcommands-only (not the asked-for experience) |
| D-003 | Deletion: **Trash by default**, `--permanent` opt-in, `--dry-run`, always confirm | permanent default, trash-only |
| D-004 | AI providers v1: **Claude + OpenAI + Ollama** behind one `Provider` interface; tool fully usable with no key (heuristics only) | Claude-only; generic OpenAI-compatible only |
| D-005 | Editors scanned: **VS Code, Insiders, Cursor, VSCodium, Windsurf** | VS Code only |
| D-006 | API keys in **macOS Keychain** (env var override) | 0600 config file |
| D-007 | Repo: **`~/Projects/purge`**, private GitHub repo, name/binary **`purge`** | public, local-only |
| D-008 | Subscription: **no-op `license.Check` seam now**, implemented in M6 only | — |
| D-009 | Local models: choosing Ollama opens a **model picker** that lists installed models, recommends models sized to the Mac's RAM/chip from a curated catalog, and pulls the chosen one with progress via Ollama's local API | free-text model name only; auto-pulling a default without asking |

Embedded in this plan after checking official docs today (say so if you disagree):

| ID | Decision | Why |
|---|---|---|
| D-010 | Charm **v2** (`charm.land/bubbletea/v2` 2.1.0, `bubbles/v2` 2.2.1, `lipgloss/v2` 2.0.6, `huh/v2` 2.0.3); Cobra 1.10.2; `sahilm/fuzzy` 0.1.3; `howett.net/plist` | v2 stable since 2026-02; a new project should not start on v1 |
| D-011 | Trash: exec `/usr/bin/trash` (macOS 15+), fall back to Finder via `osascript` on macOS 14. Minimum supported macOS: 14 | keeps the build cgo-free; `trash` first appeared in macOS 15 |
| D-012 | Keychain via `zalando/go-keyring` 0.2.8 (shells out to `/usr/bin/security`, no cgo) | `keybase/go-keychain` needs cgo and a stable codesign identity |
| D-013 | LLM clients: official **`anthropic-sdk-go` 1.82.0** (structured outputs via `output_config`, GA) and **`openai-go/v3` 3.82.0** (Responses API + strict JSON schema); **Ollama via `net/http`** on native `/api/chat` with a JSON schema in `format` | SDKs track fast-moving APIs; Ollama's Go package pulls in the whole server module |
| D-014 | Registry enrichment behind `--online`, off by default: VS Code → Marketplace `extensionquery`; Cursor/Windsurf/VSCodium → Open VSX REST | heuristics never depend on network; the forks ship Open VSX |
| D-015 | Release: GoReleaser 2.18.3 with `homebrew_casks` and quill-based `notarize.macos` | `brews` is hard-deprecated since 2.16; `macos_native` notarize is Pro-only |
| D-016 | Extension dirs resolved from each editor's `product.json` → `dataFolderName`, fallback list `~/.vscode`, `~/.vscode-insiders`, `~/.cursor`, `~/.vscode-oss`, `~/.windsurf` | only VS Code's path is officially documented |

Defaults I'm applying without asking (say so if you disagree):
- Go pinned in `go.mod` as `go 1.27.0` / `toolchain go1.27.3` (latest stable). Go is not installed here yet → `brew install go` in M0.
- System apps (`/System/Applications`, `com.apple.*`, SIP-restricted) are listed but never removable.
- Homebrew cask apps: offer `brew uninstall --zap`, our sweep, or both (default both).
- Root-owned leftovers under `/Library`: one admin prompt via `osascript … with administrator privileges`; `--sudo` for headless. Tool refuses to run as root.
- LLM receives only heuristic findings + bounded code snippets (~24 KB), never whole extensions. Structured JSON verdict.
- Marketplace metadata (verified publisher, install counts) is optional enrichment behind `--online`, off by default.
- Login items are detected and reported, not removed (no public API).
- Distribution: GitHub Releases via GoReleaser + Homebrew tap. rootout is a command-line tool, not a standard macOS application: no App Store, no app bundle, no DMG, no installer. Signing/notarizing the bare binary is only there to avoid Gatekeeper warnings on download (decide at M5). See D-024.

## Architecture

```
cmd/purge/main.go            cobra root; bare command -> tui
internal/
  safety/    protected prefixes, generic-name denylist, Validate(path)  <- every deletion passes here, twice
  fsx/       Env{Home, Root}, size walk, lstat helpers (tests point Env at temp dirs)
  app/       Discover() .app bundles (Info.plist via howett.net/plist), Fuzzy()
  leftover/  rules table (dir -> match key) + Find(app) -> []Leftover{Path, Kind, MatchedBy, Size, NeedsRoot}
  brew/      CaskFor(app), Zap(cask)
  trash/     Move(paths): /usr/bin/trash when present, else Finder via osascript
  priv/      Run(paths) once with admin rights; argv list, re-validated, no shell interpolation
  plan/      RemovalPlan, Build(), Execute(), Confirmer interface (tui | prompt | --yes)
  ext/       Enumerate() extensions for 5 editors (product.json dataFolderName), parse package.json + extensions.json + .obsolete, Uninstall()
  registry/  optional --online enrichment: Marketplace extensionquery + Open VSX clients
  heur/      walker with size caps, rule registry (pure funcs), Finding, Report{Score 0-100}
  llm/       Provider interface, Assessment JSON, BuildPrompt(report, maxBytes), fake + anthropic/openai/ollama adapters
  llm/ollama/ Detect, ListInstalled, Recommend (catalog.go), Pull, Hardware probe
  keychain/  Get/Set/Delete via zalando/go-keyring (interface injected for tests)
  config/    ~/.config/purge/config.toml: provider, model, defaults (never secrets)
  license/   Check(ctx) Status — always Allowed until M6
  output/    human table + JSON renderers shared by subcommands
  tui/       Bubble Tea models: app list/search, removal confirm (per-item toggles), scan results, model picker
  testutil/  FakeHome(t, spec) builds a fake ~/Library and extension trees; RequireSandbox(t, env)
```

Key types:
```go
type App struct{ Name, BundleID, Path, Version, TeamID, Cask string; SizeBytes int64; Protected bool }
type Leftover struct{ Path string; Kind Kind; MatchedBy string; SizeBytes int64; NeedsRoot bool }
type RemovalPlan struct{ App App; Items []Leftover; UseBrewZap bool; Mode Mode } // Trash | Permanent | DryRun
type Confirmer interface{ Confirm(p RemovalPlan) (RemovalPlan, bool, error) }   // may return an edited plan
type Extension struct{ Editor, ID, Publisher, Name, DisplayName, Version, Dir, Main string; ActivationEvents []string; Scripts map[string]string; Orphaned bool }
type Finding struct{ Rule string; Severity Severity; File string; Line int; Snippet, Detail string }
type Report struct{ Ext Extension; Score int; Findings []Finding }
type Assessment struct{ Verdict string; Confidence float64; Reasoning string; IOCs []string }
type Provider interface{ Name() string; Assess(ctx context.Context, r Report) (Assessment, error) }
```

Key invariant: `tui/` and subcommands do no work themselves. Both call `app.Discover → plan.Build → Confirmer → plan.Execute` and `ext.Enumerate → heur.Scan → llm.Assess → ext.Uninstall`. Only confirmation UI and rendering differ.

### Leftover-discovery rules (B = exact bundle id, B* = `B.` prefix, T.B = TeamID-prefixed, N = exact name with guard)

| Location | Key |
|---|---|
| `~/Library/Preferences/<B>.plist`, `<B>.*.plist`, `ByHost/<B>.*` | B, B* |
| `~/Library/Caches/<key>`, `Logs/<key>`, `Application Support/<key>` | B, B*, N (guarded) |
| `~/Library/Containers/<key>` | B, T.B |
| `~/Library/Group Containers/<key>` | T.B, `group.<B>` only; never N |
| `~/Library/Saved Application State/<B>.savedState`, `HTTPStorages/<B>*`, `Cookies/<B>.binarycookies`, `WebKit/<B>`, `Application Scripts/<B>` | B |
| `~/Library/LaunchAgents`, `/Library/LaunchAgents`, `/Library/LaunchDaemons` | B* or plist `Program`/`ProgramArguments[0]` inside the .app |
| `/Library/PrivilegedHelperTools/<B>*` | B* (root) |
| `/Library/Application Support/<N>`, `/Library/Preferences/<B>.plist`, `/Library/Caches/<B>` | B, N guarded (root) |
| `~/Library/Logs/DiagnosticReports/<N>-*.ips` | N exact prefix |
| pkg receipts (`pkgutil --pkgs` filtered by B*) | list files still present; `--forget` on execute |
| Login items (`sfltool dumpbtm`) | report only |
| `~/.config/<N>`, `~/.<N>` | N guarded, **off by default** (`--dotfiles`) |

Name guard: `len(name) >= 4`, not in denylist (every top-level `~/Library` dir name, Apple app names, `R`, `Go`, `Java`, vendor names…), and the name must be corroborated by the bundle id (`com.vendor.<name-ish>`). Exact directory-name equality only, case-insensitive. Every match carries `MatchedBy`; name matches render in a distinct colour in the confirm view. Users extend a plan manually with `--also <path>` or the TUI "add path" action (still passes `safety.Validate`).

### Safety invariants
1. Refuse to start as root.
2. Every planned path: absolute, cleaned, under an allowed prefix (`/Applications`, `~/Applications`, `~/Library`, `/Library`, opt-in `~/.config`), never under `/System /usr /bin /sbin /private/var/db /Library/Apple`, never an allowed prefix itself or a top-level child of `~/Library` or `/Library`, `Lstat` not followed, depth ≥ 3 under `$HOME`.
3. Protected apps are shown, never removable.
4. `safety.Validate` runs at plan build and again inside `Execute` before each deletion.
5. Privileged step: fixed argv list, re-validated, single `osascript` call; quoting tested against names with `'"$;` and spaces.
6. No deletion without confirmation unless `--yes`; `--yes --permanent` prints a red banner.
7. `heur` never executes extension code, never follows symlinks out of the extension dir, caps file sizes (2 MB/file, 50 MB or 2000 files/extension).
8. Tests inject `Env`; `testutil.RequireSandbox(t, env)` fails any test pointed at the real home.
9. LLM prompt contains findings + snippets only; `--json` never prints keys.

### Extension heuristics (each rule a pure func with severity + per-rule cap; sourced from 2024–2026 reports: GlassWorm, Checkmarx, Aqua, ExtensionTotal, Microsoft, CSA)
- **A. Code:** `eval` / `new Function` / `child_process` with string-built or decoded arguments; obfuscation markers (`_0x` identifiers, hex-escaped strings, very long lines, high entropy); large base64 blobs; **invisible Unicode payloads** (runs of variation selectors U+FE00–FE0F / U+E0100–E01EF or Private Use Area code points in source); remote download → `chmod +x` → spawn chains; dynamic `require` of http/net/dns.
- **B. Exfil / credentials:** raw IPs, non-TLS beacons, Discord/Telegram webhooks, pastebin, ngrok, Solana/DHT/calendar-style C2 patterns, non-marketplace update URLs; reads of `~/.ssh`, `~/.aws`, `~/.npmrc`, `~/.gitconfig`, `.env`, browser profiles, wallet paths, `~/.lc`-style token files, `security find-generic-password`, `process.env` dumps; hosts outside the publisher's domain.
- **C. Metadata:** `postinstall` / `preinstall` scripts; `activationEvents: ["*"]` or `onStartupFinished` combined with shell/network usage; display name vs publisher mismatch; typosquat (edit distance ≤ 2) against a bundled list of ~100 popular ids; `repository` URL pointing at another publisher; native `.node` binaries in non-native-purpose extensions; missing VSIX signature file; with `--online`: unverified publisher (`isDomainVerified:false`), publisher age < 6 months, tiny or spiked install counts, Open VSX `verified`/`publishedWithTrustedPublishing` false.
- **D. Hygiene (from today's audit):** orphaned folder (in `.obsolete` or absent from `extensions.json`); stale > 2 years with bundled `node_modules`; bundled deps below a small built-in advisory table; AI-agent wrappers launching tools with `--permission-mode bypassPermissions` / `--dangerously-skip-permissions`; plaintext token files written under `~`.
- Score = capped weighted sum with a minification discount for an allowlist of ~50 top publishers; reasons always shown; `--min-score` (default 30) selects flagged extensions for LLM review.

### LLM assessment
`Assess(ctx, Report) → Assessment{verdict: benign|suspicious|malicious|unknown, confidence, reasoning, iocs}`. Prompt = extension metadata + findings + snippets, byte-budgeted (~24 KB). Anthropic: `client.Messages.New` with `OutputConfig{Format: JSONOutputFormatParam{Schema}}`, default model `claude-sonnet-5-5` (configurable). OpenAI: `client.Responses.New` with strict JSON-schema text format, default `gpt-5.2`. Ollama: `POST /api/chat`, `stream:false`, `format: <schema>`. No forced tool-choice (returns 400 on current Claude models). Key resolves Keychain → env var (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`); missing key degrades to "LLM unavailable: run `purge config set-key <provider>`". Timeouts and parse failures degrade per extension, never abort the scan.

### Local models (Ollama) — picker, recommender, guided install

Entry points: TUI "Assess with AI → Local (Ollama)", `purge config models` (list + recommend), `purge config pull <model>`, and `purge scan --provider ollama` when no model is configured yet. All go through one `internal/llm/ollama` package:

```go
type Hardware struct { Chip string; AppleSilicon bool; RAMBytes int64; Cores int }   // sysctl hw.memsize, machdep.cpu.brand_string, hw.optional.arm64
type Installed struct { Name, Family, ParameterSize, Quantization string; SizeBytes int64; FitsRAM bool }
type Recommendation struct { Name string; DownloadBytes int64; Why string; Fit string /* "comfortable" | "tight" | "too large" */ }
func Detect(ctx, baseURL) (version string, err error)       // GET /api/version; ErrNotRunning / ErrNotInstalled
func ListInstalled(ctx, baseURL) ([]Installed, error)        // GET /api/tags → models[].name, size, details.parameter_size, details.quantization_level
func Recommend(hw Hardware, installed []Installed) []Recommendation   // pure; curated catalog in catalog.go
func Pull(ctx, baseURL, model string, progress func(status string, completed, total int64)) error   // POST /api/pull, stream:true
```

Flow:
1. **Detect.** `GET http://localhost:11434/api/version`. If unreachable: look for `ollama` on PATH or `/Applications/Ollama.app`. Installed but not running → offer to start it (`ollama serve` in background, or `brew services start ollama` when it is the brew formula). Not installed → show the two official routes (`brew install ollama` or the Ollama.app download) and offer to run the brew command after confirmation; never install silently.
2. **List installed** from `/api/tags`, showing name, parameter size, quantization, disk size, and a `FitsRAM` badge. The configured default (`config.llm.ollama.model`) is pre-selected. Picking one sets it as default and returns to the scan.
3. **Recommend** when nothing is installed, or always under an "Install a model…" entry. Hardware probe via `sysctl`; fit rule: estimated resident size (model file size × ~1.2 for KV cache/overhead) ≤ 60 % of RAM = comfortable, ≤ 80 % = tight, else too large (shown greyed with the reason). Intel Macs get a "CPU-only, slow" warning and are capped at the smallest tier. Catalog is a small Go table keyed by RAM tier: 8 GB → ~3 B models (`qwen2.5-coder:3b`, `llama3.2:3b`), 16 GB → 7–8 B (`qwen2.5-coder:7b`, `llama3.1:8b`), 32 GB → ~14 B, 64 GB+ → ~32 B. Each entry carries download size and a one-line "why" (code-analysis quality, JSON-mode reliability). **Model names and sizes above are from memory and must be verified against ollama.com/library during M4; the catalog is data, so updating it is a one-file change.** This machine (M1, 8 GB) lands in the first tier, so that tier gets tested first.
4. **Guided pull.** `POST /api/pull` with `stream: true`; render `completed/total` as a progress bar in the TUI and as a single updating line in the CLI. On `"success"`, save as default, run a 1-token smoke request, then continue. Failures (disk full, network) show the error and return to the picker; nothing is half-configured.
5. **No-TTY / `--json`:** `purge config models --json` prints installed + recommendations; `purge config pull <model> --yes` is non-interactive. `purge doctor` reports Ollama status, installed models, and hardware tier.

Config keys: `llm.ollama.base_url` (default `http://localhost:11434`), `llm.ollama.model`. Keys are never involved for Ollama.

### Trash
`/usr/bin/trash [-v] [-s] FILE...` ships with macOS 15+ (man page: "first appeared in macOS 15.0"; present on this machine). `trash.Move` execs it when present; on macOS 14 it falls back to `osascript -e 'tell application "Finder" to delete POSIX file "…"'` (first use triggers an Automation permission prompt, documented in setup.md). No cgo, no `NSFileManager`. Root-owned items in `/Applications` go through the privileged step instead.

## Milestones and tasks

`[P]` = independent of other `[P]` tasks in the same milestone → parallel subagents. Each subagent gets its task, `docs/kb/conventions.md`, and the toolchain line.

**M0 Scaffold + knowledge base**
1. `brew install go`; `go mod init github.com/<user>/purge`; `go 1.27.0` / `toolchain go1.27.3` in `go.mod`; `Makefile` (`build`, `test`, `lint` with golangci-lint, `CGO_ENABLED=0`); `.gitignore`; git init; private GitHub repo.
2. Cobra root with persistent `--json`, stub subcommands, `license.Check` in `PersistentPreRun`.
3. `[P]` `fsx.Env` + `testutil.FakeHome` declarative builder + `RequireSandbox`.
4. `[P]` `safety` package with table tests (hostile paths, symlinks, generic names).
5. `CLAUDE.md`, `docs/kb/{README,architecture,conventions,setup}.md`, `docs/DECISIONS.md` (D-001…D-016), `docs/HANDOVER.md`. Move `~/Desktop/docs/vscode-extension-audit` into `docs/research/` and save today's library/indicator research report alongside it.

**M1 List + search TUI**
1. `[P]` `app.Discover` (plist parsing, size, cask detection via Caskroom, Protected flag) with fixture `.app` dirs.
2. `[P]` `app.Fuzzy` ranking (exact id > exact name > prefix > fuzzy).
3. `[P]` `output` table/JSON renderers.
4. `purge list` wired + golden JSON test.
5. `tui` list model: search input, list, preview pane (name, id, path, size, cask tag, protected badge).

**M2 Leftovers + remove**
1. `[P]` `leftover` rules as data + one golden test per rule row on FakeHome, including negative tests (`Mail`, `R`, `Code`).
2. `[P]` `trash.Move` (`/usr/bin/trash`, Finder fallback, injected runner).
3. `[P]` `priv.Run` + quoting, runner injected, hostile-path tests.
4. `[P]` LaunchAgent/Daemon plist parsing, `launchctl bootout`, `pkgutil` receipts, `sfltool` login-item report.
5. `plan.Build/Execute`: Confirmer, modes, double validation, quit-running-app step, brew zap ordering.
6. `purge remove <app>` + TUI confirm screen (grouped by kind, sizes, per-item toggles, cask choice, add path).
7. Manual QA on a throwaway macOS user account against 3 real apps (one cask, one App Store, one pkg-installed); record in `docs/kb/qa.md`.

**M3 Extension heuristics**
1. `[P]` `ext.Enumerate` for 5 editors (`product.json` `dataFolderName` + fallback list), `extensions.json` + `.obsolete` parsing, `ext.Uninstall` (editor CLI first, dir removal + manifest rewrite fallback, warn if the editor is running).
2. `[P]` `heur` engine: walker, caps, registry, scoring, publisher allowlist.
3. `[P]` rule group A (code), `[P]` group B (exfil/credentials), `[P]` group C (metadata), `[P]` group D (hygiene). One subagent each.
4. Golden corpus: 9 synthetic extensions (benign-minified, obfuscated stealer, invisible-Unicode loader, typosquat, webhook beacon, postinstall dropper, stale-deps, orphan, permission-bypass agent) with expected scores and findings.
5. `purge scan` table/JSON + TUI results view with select-and-uninstall.
6. `[P]` `registry` package for `--online`: Marketplace `extensionquery` (flags 912, api-version 3.0-preview.1) and Open VSX `/api/{ns}/{name}`, recorded-HTTP tests, editor → registry routing.

**M4 LLM providers + Keychain + config**
1. `[P]` `keychain` via `zalando/go-keyring` behind a small interface, fake in tests.
2. `[P]` `config` TOML + `purge config set-key|show|set|set-provider`.
3. `[P]` `llm.BuildPrompt` with byte budget, JSON schema instruction, fake provider, golden prompt test.
4. `[P]` adapters: Anthropic (`anthropic-sdk-go`, structured outputs), OpenAI (`openai-go/v3`, Responses + JSON schema), Ollama (`net/http`, `/api/chat` with schema), each with a recorded-HTTP test.
5. `[P]` `ollama` package: `Detect`, `ListInstalled`, `Pull` against an `httptest` fake server replaying `/api/version`, `/api/tags`, `/api/pull` streams; `Hardware` probe with injected `sysctl` runner.
6. `[P]` `ollama.Recommend` + `catalog.go`: table tests per RAM tier (8/16/32/64 GB, Intel), fit thresholds, already-installed entries excluded; catalog entries verified against ollama.com/library and the verification URL recorded in `docs/kb/local-models.md`.
7. Model picker: TUI screen (installed list → recommend → pull with progress bar) and `purge config models` / `purge config pull <model>`; install-Ollama guidance with confirm-before-run for `brew install ollama`.
8. `--llm` / `--provider` / `--model` on `scan`; TUI "Assess with AI" action routes to the picker when provider is Ollama and no model is configured; graceful no-key path; `purge doctor` (includes Ollama status, models, hardware tier).

**M5 Release pipeline** (a CLI release, not a macOS app release; D-024)
Spec and task plan: `docs/plans/m5-release.md` (D-030…D-032).

**M6 Membership gate (last; gets its own plan; D-024)**
Intent fixed now, mechanics decided then:
- Shivang (admin and creator) uses every feature unrestricted, always: an admin credential (e.g. a signed admin token in the Keychain, or an admin build) makes `license.Check` return Allowed for everything, offline.
- Everyone else uses the CLI only with an active monthly membership paid through Patreon (to be explored when we get there). Mechanics to evaluate at M6: Patreon's API/OAuth to verify an active pledge and tier, versus membership-linked license keys (Ed25519-signed, stdlib `crypto/ed25519`) issued to patrons; which commands are gated (everything vs keeping `list`/`doctor`/dry-runs free); an offline grace period so a lapsed network does not lock a paying user out; what a non-member sees (a clear message with the Patreon link, no dark patterns).
- Not a standard macOS application distribution: no App Store, no in-app purchase; the gate lives in the CLI and the release pipeline (M5) stays a plain binary + Homebrew tap.
- `license.Check` is already called once from the Cobra root (D-008), so M6 needs no restructuring.

## Libraries (verified against official docs on 2026-10-20)

| Purpose | Module | Version |
|---|---|---|
| Toolchain | Go | 1.27.3 (go.mod `go 1.27.0`, `toolchain go1.27.3`) |
| TUI | `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`, `charm.land/huh/v2` | 2.1.0, 2.2.1, 2.0.6, 2.0.3 |
| CLI | `github.com/spf13/cobra` | 1.10.2 |
| Fuzzy | `github.com/sahilm/fuzzy` | 0.1.3 |
| plist | `howett.net/plist` | latest |
| Keychain | `github.com/zalando/go-keyring` | 0.2.8 |
| Anthropic | `github.com/anthropics/anthropic-sdk-go` | 1.82.0 |
| OpenAI | `github.com/openai/openai-go/v3` | 3.82.0 (needs Go ≥ 1.26) |
| Ollama | stdlib `net/http` against `localhost:11434` | — |
| TOML | `github.com/BurntSushi/toml` | latest |
| Release | GoReleaser | 2.18.3 |

Each is re-checked against its docs when the task that uses it starts; versions recorded in `docs/kb/setup.md`.

## Open risks
- Finder-trash fallback asks for Automation permission on first use (macOS 14 only).
- go-keyring reads may trigger a Keychain "allow" prompt because `security` is the caller; documented in setup.md.
- Marketplace / Open VSX responses can change; enrichment is optional and isolated in `registry/`.
- Notarization (optional, M5) needs a paid Apple Developer account; without it, downloaded binaries need `xattr -d com.apple.quarantine` or a Homebrew install (brew strips quarantine for casks it fetches).
- Ollama catalog model names are data to be verified in M4, not facts yet.

## Verification
- `make test` green at every milestone; `go vet` + golangci-lint clean; `CGO_ENABLED=0 go build` produces a static binary.
- M1: `purge list --json | jq length` equals the number of `.app` bundles in `/Applications` + `~/Applications` (35 at planning time); TUI search for "Spot" shows Spotify.
- M2: `purge remove <app> --dry-run` on a cask app lists bundle + leftovers with `MatchedBy`; on a throwaway user account, `purge remove` actually moves them to Trash and `find ~/Library -iname '*<bundleid>*'` returns nothing; `purge remove Mail` / `purge remove Safari` refuse.
- M3: `purge scan --json` on this machine reproduces today's audit: Microsoft/verified extensions score low; the golden corpus scores match expectations; `--online` on `esbenp.prettier-vscode` reports a verified publisher.
- M4: `purge scan --llm --provider ollama` works with no API key against a local model; `--provider anthropic` with a Keychain key returns structured verdicts; with no key it prints the hint and still outputs heuristics.
- M4 local models, on this M1 / 8 GB machine with Ollama absent: `purge config models` reports "Ollama not installed" with the two install routes and offers `brew install ollama`; after install, lists zero models and recommends the 8 GB tier with larger models greyed as "too large"; `purge config pull qwen2.5-coder:3b` shows progress and sets the default; `purge config models --json` shows it as installed with `FitsRAM: true`; a `scan --llm --provider ollama` then returns verdicts. Unit: `Recommend` table tests for all tiers pass without network.
- M5: tagged release produces binaries installable with `brew install <tap>/rootout` (notarized if M5 decides so).
- M6: with the admin credential every command works offline; without a membership the gated commands print the membership message and exit 4; with a verified membership they work; a lapsed network inside the grace period still works.
- Playbook: `scripts/toolchain_check.sh` passes; `docs/DECISIONS.md`, `docs/kb/`, `docs/HANDOVER.md` exist and are current.

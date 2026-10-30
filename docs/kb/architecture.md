# Architecture

## Shape

```
cmd/rootout/main.go            cobra root; bare command -> tui home screen
cmd/rootout/gate.go            membership gate at each gated action
cmd/rootout-keys/              offline key tool: keygen, sign (admin and test keys)
service/                       Cloudflare Worker key service (TypeScript, not part of the Go build)
internal/
  safety/    protected prefixes, generic-name denylist, Validate(path)  <- every deletion passes here, twice
  fsx/       Env{Home, Root}, size walk, lstat helpers (tests point Env at temp dirs)
  app/       Discover() .app bundles (Info.plist via howett.net/plist), Fuzzy()
  leftover/  rules table (dir -> match key) + Find(app) -> []Leftover; FindOrphans (D-020)
  brew/      CaskFor(app), Zap(cask)
  trash/     Move(paths): /usr/bin/trash when present, else Finder via osascript
  priv/      Run(paths) once with admin rights; argv list, re-validated, no shell interpolation
  tcc/       FullDiskAccess(env) probe (lists ~/Library/Safari etc.), NeedsFullDiskAccess(path), TerminalApp (D-026)
  plan/      RemovalPlan, Build(), Execute(), Confirmer interface (tui | prompt | --yes)
  ext/       Enumerate() extensions for 5 editors, parse package.json + extensions.json + .obsolete, Uninstall()
  registry/  optional --online enrichment: Marketplace extensionquery + Open VSX
  heur/      walker with size caps, rule registry (pure funcs), Finding, Report{Score 0-100}
  llm/       Provider interface, Assessment, BuildPrompt, fake + anthropic/openai/ollama adapters
  llm/ollama/ Detect, ListInstalled, Recommend (catalog.go), Pull, Hardware probe
  keychain/  Get/Set/Delete via zalando/go-keyring behind an interface
  config/    ~/.config/rootout/config.toml: provider, model, defaults (never secrets)
  license/   Resolve(ctx, Deps) State: key codec, machine id, store, renewal client, trial (D-034)
  progress/  Reporter (Start/Step/Done), Term renderer for stderr, Recorder for tests
  output/    human table + JSON renderers shared by subcommands
  tui/       Bubble Tea models: home screen (action rows + flash of the last outcome), app picker (loads inside the
             program), removal confirm (typed delete, groups), scan results (Assessor header, embedded model picker),
             model picker; shared typedConfirm, ? overlay, bar; ErrQuit (ctrl+x/ctrl+c) ends everything
  testutil/  FakeHome(t, spec), RequireSandbox(t, env)
```

Invariant: `tui/` and subcommands do no work themselves. Both call the same core
pipelines; only how confirmation is collected and how output is rendered differ.

## Main flows

**home**: `tui.PickAction` → one of the flows (uninstall = picker → review → `plan.Execute`; orphans =
`findOrphans` → review → execute; scan = `scanReports` → review → uninstall; model = `pickAndSaveModel`) → flash
back on the home screen. `tui.ErrQuit` (ctrl+x/ctrl+c) ends the loop; subcommands map it to the same exit as esc.

**remove**: `app.Discover` → match arg (exact bundle id > exact name > fuzzy) →
`plan.Build` (`safety.CheckApp` → `leftover.Find` → `safety.Validate` each path →
sizes → `NeedsRoot` flags → `brew.CaskFor`) → `Confirmer.Confirm` (shows every item
grouped by kind with size and `MatchedBy`; user may untick or add paths) →
`plan.Execute` (quit app → `launchctl bootout` agents → brew zap if chosen →
user-owned items via `trash.Move` or `os.RemoveAll` → one `priv.Run` for root-owned
items → `pkgutil --forget`) → `Result`.

**scan**: `ext.Enumerate` (5 editors) → `heur.Scan` per extension (bounded workers,
file caps) → sort by score → optional `registry` enrichment (`--online`) → optional
`llm.Assess` for extensions ≥ `--min-score` (`--llm`) → selection → confirm →
`ext.Uninstall` (editor CLI first, directory removal + manifest rewrite fallback).

**orphans**: `app.DiscoverWith(SkipSizes)` → `leftover.FindOrphans` (owned set: installed ids, Apple, SDK list,
Spotlight bundles, launchd, processes) → `plan.New` grouped by owner, nothing selected → confirm screen → typed
`delete` → `plan.Execute`.

**local model picker**: `ollama.Detect` → `ListInstalled` → `Recommend(hardware)` →
`Pull` with progress → save `llm.ollama.model` in config.

## Membership gate

`license.Resolve` reads the key file, checks the signature and that the key's machine id matches this Mac, then returns a state: admin (offline, always allowed), member, trial, grace, expired, trial-used, none, invalid or no-machine-id; a 403 from the Worker surfaces as expired with its reason. It renews a patron key through the Worker when under 7 days remain or when expired, throttled by a stamp beside the key (24 h, or 1 h once expired with no remembered 403). With no key it can start a trial.

The gate sits at the action, in `cmd/rootout/gate.go`, because the TUI home loop runs actions in-process. Gated: `remove`, `orphans`, `scan --uninstall`, the review-screen uninstall and every LLM assessment. Free and licence-call-free: `list`, `doctor`, `guide`, `--dry-run` and heuristic `scan`; doctor and guide only read the key file and `ioreg`. A denial exits 4. The TUI starts a trial only after the user confirms a deletion; a subcommand asks y/n (`--yes` skips); the LLM path never starts one.

The Worker (`service/`) is the only external component and the only licence-related URL in the binary. Routes: `GET /` and `GET /start` (key page, Patreon sign-in), `GET /callback` (OAuth, issues a patron key), `POST /renew` (no KV writes) and `POST /trial` (once per machine id). It signs with the private key held as a Worker secret; the binary embeds the public key. `rootout-keys sign` makes keys outside the Worker; they never renew.

## Data

- No database. State is the filesystem plus `~/.config/rootout/config.toml`.
- Secrets only in Keychain (service `rootout`, account = provider name).
- Key types live in the package that owns them: `app.App`, `leftover.Leftover`,
  `plan.RemovalPlan`, `ext.Extension`, `heur.Finding/Report`, `llm.Assessment/Provider`.

## Boundaries

- Never touches `/System`, `/usr`, `/bin`, `/sbin`, `/private/var/db`, `/Library/Apple`.
- Login items are reported, not removed.
- Marketplace/Open VSX lookups are optional; scoring never depends on them.
- Deletion and LLM features are gated by `license.Resolve` at the action (D-034); read-only features never call the licence service.

# Architecture

## Shape

```
cmd/purge/main.go            cobra root; bare command -> tui
internal/
  safety/    protected prefixes, generic-name denylist, Validate(path)  <- every deletion passes here, twice
  fsx/       Env{Home, Root}, size walk, lstat helpers (tests point Env at temp dirs)
  app/       Discover() .app bundles (Info.plist via howett.net/plist), Fuzzy()
  leftover/  rules table (dir -> match key) + Find(app) -> []Leftover
  brew/      CaskFor(app), Zap(cask)
  trash/     Move(paths): /usr/bin/trash when present, else Finder via osascript
  priv/      Run(paths) once with admin rights; argv list, re-validated, no shell interpolation
  plan/      RemovalPlan, Build(), Execute(), Confirmer interface (tui | prompt | --yes)
  ext/       Enumerate() extensions for 5 editors, parse package.json + extensions.json + .obsolete, Uninstall()
  registry/  optional --online enrichment: Marketplace extensionquery + Open VSX
  heur/      walker with size caps, rule registry (pure funcs), Finding, Report{Score 0-100}
  llm/       Provider interface, Assessment, BuildPrompt, fake + anthropic/openai/ollama adapters
  llm/ollama/ Detect, ListInstalled, Recommend (catalog.go), Pull, Hardware probe
  keychain/  Get/Set/Delete via zalando/go-keyring behind an interface
  config/    ~/.config/purge/config.toml: provider, model, defaults (never secrets)
  license/   Check(ctx) Status — always Allowed until M6
  output/    human table + JSON renderers shared by subcommands
  tui/       Bubble Tea models: app list/search, removal confirm, scan results, model picker
  testutil/  FakeHome(t, spec), RequireSandbox(t, env)
```

Invariant: `tui/` and subcommands do no work themselves. Both call the same core
pipelines; only how confirmation is collected and how output is rendered differ.

## Main flows

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

**local model picker**: `ollama.Detect` → `ListInstalled` → `Recommend(hardware)` →
`Pull` with progress → save `llm.ollama.model` in config.

## Data

- No database. State is the filesystem plus `~/.config/purge/config.toml`.
- Secrets only in Keychain (service `purge`, account = provider name).
- Key types live in the package that owns them: `app.App`, `leftover.Leftover`,
  `plan.RemovalPlan`, `ext.Extension`, `heur.Finding/Report`, `llm.Assessment/Provider`.

## Boundaries

- Never touches `/System`, `/usr`, `/bin`, `/sbin`, `/private/var/db`, `/Library/Apple`.
- Login items are reported, not removed.
- Marketplace/Open VSX lookups are optional; scoring never depends on them.
- Licensing is a seam (`license.Check`) until M6.

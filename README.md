# rootout

Uninstall macOS apps completely, and find risky extensions in VS Code, Cursor,
VSCodium, Windsurf and VS Code Insiders.

```bash
rootout                       # home screen: uninstall an app, remove orphans, scan extensions, pick a model
rootout list [--search q]     # installed apps
rootout remove <app> -n       # dry run: show the bundle and every leftover file
rootout remove <app>          # move them to the Trash after you confirm
rootout scan                  # score installed extensions; review and uninstall on a terminal
rootout scan --llm            # also ask Claude, OpenAI or a local Ollama model
rootout orphans               # leftovers of apps you already deleted, grouped by old app id
rootout setup                 # optional: a local model or a Claude/OpenAI key for recommendations
rootout config models --pick  # choose or download a local model that fits this Mac
rootout doctor                # check what rootout depends on
rootout guide                 # a step-by-step tour adapted to this Mac
```

## Install

```bash
# Homebrew
brew tap shiva2021/rootout
brew trust shiva2021/rootout   # Homebrew 7 asks this once for third-party casks
brew install --cask rootout

# Go toolchain (`rootout --version` prints `dev` this way; no release metadata)
go install github.com/shiva2021/rootout/cmd/rootout@latest

# Direct download: grab rootout_<version>_darwin_<arch>.tar.gz from the Releases page, then
tar xzf rootout_*_darwin_*.tar.gz && xattr -d com.apple.quarantine rootout && mv rootout ~/.local/bin/

# From source
make install
```

The binary is not code-signed; Homebrew clears the quarantine flag for you.

## First launch

The first `rootout` opens a short setup screen: pick a free local model
through Ollama, paste a Claude or OpenAI API key (checked with one free
request, then stored in the macOS Keychain, after which you pick the model
the key may use), or choose "Not now". AI is optional: without it, scans
still score extensions with the built-in rules; with it, tick extensions in
the scan screen and press `l` to ask the model whether to keep or uninstall
them (two at a time; the right pane lists every answer). The home
screen always shows the current AI status and model, and `rootout setup`
reopens the screen.

Nothing is uninstalled until you type `delete`. In every screen `?` lists the
keys; in the scan screen `m` changes the model without leaving the scan (the
provider's hosted list, or the local Ollama picker). Scripts use
`rootout config models --provider anthropic|openai [--pick]`.
esc goes back one screen; ctrl+x quits from anywhere.

Every command takes `--json`. Removal goes to the Trash unless `--permanent`.
System apps are listed but never removable. Nothing runs as root; files that
need admin rights are removed in one step after a single macOS password prompt.

Extension scanning is static: extension code is never executed. Scores separate
what an extension can do (spawn processes, eval) from evidence of intent
(hidden payloads, credential reads, exfiltration hosts); see
`docs/kb/heuristics.md`. An AI model, if configured, sees only the findings and
short snippets, never the whole extension.

## Contributing

Issues and pull requests are welcome. Branch from `main`, keep `make test` and
`make lint` green, and read `CLAUDE.md` and `docs/kb/README.md` first: every
deletion goes through `internal/safety.Validate`, and no test may touch the
real home directory. Security reports: see `SECURITY.md`.

## Licence

MIT, see `LICENSE`.

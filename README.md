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
rootout config models --pick  # choose or download a local model that fits this Mac
rootout doctor                # check what rootout depends on
rootout guide                 # a step-by-step tour adapted to this Mac
```

## Install

```bash
# Homebrew (private tap: needs git access to shiva2021/homebrew-rootout and a GitHub token)
export HOMEBREW_GITHUB_API_TOKEN=<token with read access to shiva2021/rootout>
brew tap shiva2021/rootout git@github.com:shiva2021/homebrew-rootout.git
brew trust shiva2021/rootout   # newer Homebrew refuses untrusted taps
brew install --cask rootout

# Direct download: grab rootout_<version>_darwin_<arch>.tar.gz from the Releases page, then
tar xzf rootout_*_darwin_*.tar.gz && xattr -d com.apple.quarantine rootout && mv rootout ~/.local/bin/

# From source
make install
```

The binary is not code-signed yet; Homebrew clears the quarantine flag for you.

## Membership

rootout is free to look at and paid to delete with. Every Mac gets a 7-day
trial of the gated features, started the first time you confirm a deletion or
run `rootout license`. After that you need an active membership.

- Always free: `list`, `doctor`, `guide`, every `--dry-run` and the heuristic `scan`.
- Needs a key: `remove`, `orphans`, `scan --uninstall`, the uninstall step in the scan and home screens, and every AI assessment.
- `rootout license` shows your status, this Mac's machine code and the key page. Paste a key with `rootout license <key>`.
- A key is tied to one Mac, and an account can use two Macs.
- Keys renew themselves in the background; if renewal fails (offline or a service error) a key keeps working for 14 days past its expiry.
- `rootout doctor` shows a Membership line with the same status.

Nothing is uninstalled until you type `delete`. In every screen `?` lists the
keys; in the scan screen `m` picks a local model without leaving the scan.
esc goes back one screen; ctrl+x quits from anywhere.

Every command takes `--json`. Removal goes to the Trash unless `--permanent`.
System apps are listed but never removable. Nothing runs as root; files that
need admin rights are removed in one step after a single macOS password prompt.

Extension scanning is static: extension code is never executed. Scores separate
what an extension can do (spawn processes, eval) from evidence of intent
(hidden payloads, credential reads, exfiltration hosts); see
`docs/kb/heuristics.md`. An AI model, if configured, sees only the findings and
short snippets, never the whole extension.

Development: see `CLAUDE.md` and `docs/kb/README.md`.

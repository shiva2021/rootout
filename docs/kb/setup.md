# Setup

## Prerequisites
- Go 1.27 (pinned in `go.mod`: `go 1.27.0` / `toolchain go1.27.3`). Install with `brew install go`; with the default `GOTOOLCHAIN=auto` the pinned toolchain is fetched automatically.
- `golangci-lint` (`brew install golangci-lint`) for `make lint`.
- GoReleaser 2.19.0 (`brew install goreleaser`) for `make snapshot` and `make release-check`; CI pins the same version.
- macOS 14 or later. `/usr/bin/trash` exists on macOS 15+; macOS 14 uses the Finder fallback.
- Optional: Homebrew (cask detection), Ollama (local models), editor CLIs (`code`, `cursor`, `codium`, `windsurf`, `code-insiders`) for clean extension uninstall.

## First run
```bash
cd ~/Projects/rootout
go mod download
make build && ./bin/rootout --help
make test
scripts/toolchain_check.sh
```

## Environment variables
| Name | Purpose | Where to get it |
|---|---|---|
| `ANTHROPIC_API_KEY` | Overrides the Keychain entry for the Anthropic provider | console.anthropic.com |
| `OPENAI_API_KEY` | Overrides the Keychain entry for the OpenAI provider | platform.openai.com |
| `ROOTOUT_CONFIG` | Alternate config file path (default `~/.config/rootout/config.toml`) | — |
| `ROOTOUT_CACHE` | Alternate saved-assessments file (default `~/.cache/rootout/assessments.json`) | — |
| `GOTOOLCHAIN` | Leave at `auto` so the pinned toolchain is used | — |

Keys are normally stored with `rootout config set-key <provider>` (Keychain service `rootout`).

## Common problems
- Keychain "allow access" dialog on each read → click "Always Allow"; the caller is `/usr/bin/security`, not the binary (D-012).
- Finder asks for Automation permission (macOS 14 only) → allow once; it is the Trash fallback (D-011).
- `go: downloading go1.27.3` on first build → expected; the toolchain directive fetches the pinned version.

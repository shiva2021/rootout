# rootout

macOS CLI that uninstalls apps completely (bundle + leftovers, to Trash) and scans VS Code-family extensions for malicious behaviour with local heuristics and optional LLM assessment (Claude, OpenAI, Ollama). Go, cgo-free, Bubble Tea TUI + Cobra subcommands.

## Run and test

```bash
go version          # Go 1.27 pinned in go.mod (go/toolchain lines); GOTOOLCHAIN=auto fetches it
make build          # CGO_ENABLED=0 -> ./bin/rootout
make test           # go test ./...
make lint           # go vet + golangci-lint
./bin/rootout         # TUI; or: rootout list|remove|scan|config|doctor [--json]
```

Try it read-only: `./bin/rootout doctor`, `./bin/rootout remove <app> --dry-run`, `./bin/rootout scan --all --no-tui`.

## Where things are

- `docs/HANDOVER.md` — current state of work; read first every session.
- `docs/kb/README.md` — knowledge base index; open only the topics you need.
- `docs/DECISIONS.md` — why things are the way they are (D-001…).
- `docs/research/` — audit and library research that shaped the plan.

## Rules for this repo

- Plan and get approval before implementing. Ask, don't assume.
- Follow `docs/kb/conventions.md`; keep code simple; stdlib first.
- Every deletion goes through `internal/safety.Validate`. No exceptions.
- No test may touch the real home directory; use `testutil.FakeHome`.
- Write `docs/HANDOVER.md` before ending a session.

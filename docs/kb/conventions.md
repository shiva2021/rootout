# Conventions

Follow these so every module reads as if one person wrote it.

## Naming
- Packages: short, lowercase, singular (`app`, `leftover`, `heur`). No `util`.
- Exported identifiers only when another package needs them.
- Files: `thing.go` + `thing_test.go`; rules tables in `rules.go`; fixtures in `testdata/`.
- Errors: sentinel `var ErrX = errors.New("...")` for conditions callers branch on.

## Layout
- `cmd/rootout/` has only Cobra wiring; no logic.
- `internal/<pkg>/` owns its types. Shared cross-cutting helpers live in `fsx`, `output`, `testutil`.
- Every package that touches the filesystem takes a `fsx.Env{Home, Root}` and never calls `os.UserHomeDir()` itself. Only `cmd/` and `config/` build the real `Env`.
- External commands (`security`, `trash`, `osascript`, `brew`, `sysctl`, `launchctl`, editor CLIs) go through an injectable `Runner` so tests never exec them.

## Errors and logging
- Wrap with context: `fmt.Errorf("leftover: read %s: %w", path, err)`. Never swallow errors; never `panic` outside `main`.
- No logging to stdout. `--json` output is the only stdout in subcommands. Diagnostics go to stderr via `log/slog`, enabled by `--verbose`.
- Exit codes: 0 ok, 1 error, 2 ambiguous/invalid input, 3 user declined.

## Testing
- Stdlib `testing`, table-driven, `t.Run` subtests. No assertion library.
- Filesystem tests build a fake home with `testutil.FakeHome(t, spec)` and call `testutil.RequireSandbox(t, env)` first. No test may read or write the real home.
- Golden files under `testdata/`; update with `go test ./... -update`.
- HTTP providers are tested with `httptest.Server` or recorded JSON responses in `testdata/`.
- Run: `make test`. Coverage is expected for `safety`, `leftover`, `heur`, `plan`.

## Formatting and linting
```bash
gofmt -l .            # must print nothing
go vet ./...
golangci-lint run     # config in .golangci.yml
```

## Commits
- Imperative, under 72 chars: `leftover: add Group Containers rule`. Body explains why when non-obvious.
- One logical change per commit. Reference decisions as `D-0NN` where relevant.
- Branch `main`; feature branches `m<milestone>/<topic>` when work runs in parallel.

## Long operations and confirmations
- Every operation that can take more than a second accepts a `progress.Reporter` (nil = `progress.Nop()`), calls `Start/Step/Done`, and the command passes `stderrProgress(g)`. TUI screens that load data do it inside the program and render `renderCount`.
- Every destructive confirmation goes through `tui.typedConfirm` (screens) or `plan.Prompt`/`confirmTyped` (plain terminal); the word is `plan.ConfirmWord`. `--yes` is the only bypass and calls `yesBanner`.
- Every screen registers its keys with the `?` overlay (`renderHelp`) and ends its footer with `· ? help`.
- Every command has `Long` and `Example`.
- ctrl+x and ctrl+c quit rootout from every screen: the model sets `quitAll`, quits, and the entry point returns `tui.ErrQuit`. esc and q only end the current screen with ok=false; the caller decides what back means. Subcommands wrap TUI errors with `declinedOnQuit` so ctrl+x and esc give the same exit code.

## Releases and versions
- Every merge to `main` gets an annotated semver tag: minor for a feature (`v0.4.0`), patch for fixes or docs (`v0.3.1`). Cut it at wrap-up: `git tag -a vX.Y.Z -m "vX.Y.Z: <one line>"` then `git push origin main vX.Y.Z`.
- Tagging goes through `make release-check NEXT=vX.Y.Z` first: the pushed tag publishes a release and the release job does not run the tests. Full steps in `release.md`.
- The binary learns its version from `git describe --tags --always --dirty` (Makefile → `main.version`), so a clean checkout of a tag prints `vX.Y.Z`; anything else prints `vX.Y.Z-N-g<hash>[-dirty]`, which says how far past the tag the build is. `rootout --version`, `rootout doctor` (first line) and the home-screen title all show it; `make install` echoes what it installed.
- `CHANGELOG.md` (Keep a Changelog format) gets an entry per tag; add to its "Unreleased" section as features land.

## Safety rules that are also conventions
- Any path destined for deletion is validated by `safety.Validate` at plan time and again immediately before the delete.
- Name-based leftover matches require `safety.NameOK` and are never the sole basis for anything under `/Library` or `Group Containers`.
- Privileged execution takes a fixed `[]string` argv, never a shell string.

# Conventions

Follow these so every module reads as if one person wrote it.

## Naming
- Packages: short, lowercase, singular (`app`, `leftover`, `heur`). No `util`.
- Exported identifiers only when another package needs them.
- Files: `thing.go` + `thing_test.go`; rules tables in `rules.go`; fixtures in `testdata/`.
- Errors: sentinel `var ErrX = errors.New("...")` for conditions callers branch on.

## Layout
- `cmd/purge/` has only Cobra wiring; no logic.
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

## Safety rules that are also conventions
- Any path destined for deletion is validated by `safety.Validate` at plan time and again immediately before the delete.
- Name-based leftover matches require `safety.NameOK` and are never the sole basis for anything under `/Library` or `Group Containers`.
- Privileged execution takes a fixed `[]string` argv, never a shell string.

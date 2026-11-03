# Update check · `rootout doctor` row and a home-screen notice

_Spec written 2026-11-04. Status: implemented on `feat/update-check` the same day (D-039 logged). Kept as the record of the design; the code is the truth now._


## Context

Nothing tells a rootout user that a newer release exists. Homebrew users only find out if they happen to run `brew update` and `brew outdated`; direct-download users only by revisiting the Releases page. `rootout doctor` prints the installed version with a comment saying it answers "is this the latest?", but it never looks up the latest. D-028 introduced the version display for exactly that question and stopped halfway.

Outcome: `rootout doctor` gains a row that fetches the latest GitHub release and says whether an update is available, with the upgrade command as the fix. The home screen shows a one-line notice when a newer version exists, using a daily-cached lookup so launches stay instant and GitHub is asked at most once a day. One config key, `updates.check`, turns both off. `dev` builds (go install, no tag) never check.

## Design decisions (to record as D-039)

- **Source of truth:** `GET https://api.github.com/repos/shiva2021/rootout/releases/latest` (JSON: `tag_name`, `html_url`, `published_at`). GitHub excludes drafts and pre-releases from `latest`, which matches `prerelease: auto` in `.goreleaser.yaml`. Unauthenticated limit is 60 requests/hour per IP; the daily cache keeps us far below it.
- **Headers:** `Accept: application/vnd.github+json`, `User-Agent: rootout/<version>`. Timeout 5 s, body capped at 1 MiB (same shape as `internal/registry`).
- **Version compare:** parse `vMAJOR.MINOR.PATCH` from the start of the installed string, so `v0.7.0-3-gabc-dirty` compares as `v0.7.0`. `dev` or anything unparseable means "unknown build": no request is made. Only `latest > installed` produces a notice; a local build ahead of the last tag shows nothing.
- **Record file:** `$XDG_CACHE_HOME/rootout/update.json` when `XDG_CACHE_HOME` is set, else `~/.cache/rootout/update.json`, written atomically (temp file + rename, 0600 in a 0700 dir) like `assesscache`. `ROOTOUT_CACHE` is left alone: it names the assessments *file*, not a directory (D-038). Content: `{"version":1,"checked_at":"<RFC3339 UTC>","latest":"v0.8.0","url":"https://github.com/shiva2021/rootout/releases/tag/v0.8.0"}`. A missing, corrupt or foreign file is treated as "never checked". A failed write is `slog.Debug` only. `config cache clear` does not touch it (D-038 makes that command the one deletion outside `safety.Validate`, scoped to the assessments file; this record is overwritten, never deleted).
- **Freshness:** the home screen reuses a record younger than 24 h and otherwise fetches. A `checked_at` in the future counts as stale (a clock rollback must not freeze the record). `doctor` always fetches (the user asked) and refreshes the record.
- **Failed fetches are recorded too** (my call): the home-screen path writes `checked_at` with an empty `latest` when GitHub cannot be reached, so an offline or firewalled Mac makes one background attempt per day, not one per launch, and "asked at most once a day" holds literally. An empty `latest` in a fresh record means "no notice". `doctor` is the way to retry sooner.
- **`doctor --json` now reaches the network:** up to 5 s offline and a fail-soft row (`ok: true`, "could not reach GitHub"). Scripts that cannot accept that set `updates.check=false`. Say so in D-039 and in the command's `Long` text, which also needs "writes one small record under ~/.cache/rootout" next to its "changes nothing" claim.
- **Config key:** `updates.check` (bool, default `true`) in a new `[updates]` table. `false` disables both the doctor row's request and the home-screen check; doctor then prints the row as "off (rootout config set updates.check true)". Consequence to record: `config.Save` writes the whole struct, so the first save by this version writes `[updates]` and an older binary, whose loader rejects unknown keys, would refuse that file on a downgrade. Same class of consequence as D-035's frozen defaults; accepted.
- **Upgrade hint:** when `os.Executable()` (after `filepath.EvalSymlinks`) contains `/Caskroom/rootout/`, the fix is `brew update && brew upgrade --cask rootout` (the `brew update` first is the stale-tap lesson from v0.6.0). Otherwise the fix is the release URL from the response. Verified on this Mac: `/opt/homebrew/bin/rootout` is a symlink to `/opt/homebrew/Caskroom/rootout/0.7.0/rootout` (the cask's `binary` stanza links, it does not copy), and a `make install` copy under `~/.local/bin` correctly gets the URL.
- **Network policy:** the home screen is the first place rootout contacts the network without an explicit flag or key. It is one GET to GitHub, at most daily, off with one key, never for `dev` builds, and never blocks the UI (the result arrives asynchronously). Scans and heuristics stay offline (D-014). Write this down in D-039.

## Implementation

### 1. New package `internal/update`

Files: `internal/update/update.go`, `internal/update/update_test.go`.

```go
const (
    DefaultURL  = "https://api.github.com/repos/shiva2021/rootout/releases/latest"
    MaxAge      = 24 * time.Hour
)

type Release struct {
    Tag         string    `json:"latest"`
    URL         string    `json:"url"`
    PublishedAt time.Time `json:"published_at,omitzero"`
}

type Client struct {
    HTTP      *http.Client   // New(): Timeout 5s
    URL       string         // tests point it at httptest
    UserAgent string         // "rootout/<version>"
    Now       func() time.Time // nil = time.Now
}

func New(version string) *Client
func (c *Client) Latest(ctx context.Context) (Release, error)   // GET, decode tag_name/html_url/published_at; non-2xx -> "update: GET …: HTTP %d"

func Path(env fsx.Env, getenv func(string) string) string          // record path (XDG_CACHE_HOME or ~/.cache/rootout/update.json)
func ReadRecord(path string, now time.Time) (Release, bool)        // ok=false when missing, corrupt, foreign version, or older than MaxAge
func WriteRecord(path string, r Release, now time.Time) error      // atomic, 0600/0700

func Installed(version string) (v [3]int, ok bool)                 // parses leading vM.m.p; ok=false for "dev", "", garbage
func Newer(latest, installed string) bool                          // both parsed with Installed; false when either fails
```

Keep `Release` JSON tags matching the record file so one struct serves the file; the API response is decoded into a private struct (`tag_name`, `html_url`, `published_at`) and converted.

Tests (stdlib, table-driven, `httptest.NewServer`, `testutil.FakeHome` for the record path):
- `Latest`: happy path, 404, 500, malformed JSON, body over the cap, User-Agent and Accept headers sent, context timeout.
- `Installed`/`Newer`: `v0.7.0` vs `v0.8.0`, `v0.7.0-3-gabc-dirty` vs `v0.7.0` (not newer), `v0.7.0` vs `v0.7.1`, `v0.10.0` vs `v0.9.0` (numeric, not string compare), `dev`, `""`, `v1.2`.
- Record: round trip, missing file, corrupt file, `version: 2`, stale by `MaxAge`, `checked_at` in the future (stale), fresh with empty `latest` (ok, no tag), `XDG_CACHE_HOME` override, file mode 0600, `RequireSandbox` first.

### 2. Config key `updates.check`

`internal/config/config.go`:
- Add `Updates Updates \`toml:"updates"\`` to `Config` and `type Updates struct { Check bool \`toml:"check"\` }`; `Default()` sets `Check: true`.
- Add `"updates.check"` to `fields`, copying the `scan.online` get/set closures (`strconv.ParseBool`, error `updates.check: %q is not a boolean`).
- `config show`, `config get/set` and the help text pick it up from the registry automatically (`cmd/rootout/config.go` builds help from `config.Keys()`).
- Tests in `internal/config/config_test.go`: default true, round trip through Save/Load, `Set("updates.check","nope")` errors, a file without `[updates]` loads as true.

### 3. Doctor row (`cmd/rootout/doctor.go`)

`rootout guide` also calls `runChecks` (`cmd/rootout/guide.go:58`) for its "N ok, M to fix" step, and the guide's `Long` says it only reads. So the row is **not** added inside `runChecks`. Instead doctor's `RunE` calls `runChecks` and then inserts the `Update` row at index 1 (right after the `rootout` version row); the guide keeps the local-only list and never reaches the network. (My call: the plan's own rationale rejects network calls in commands nobody asked to check for updates.)

The row is built by a new helper `updateCheck(ctx, cl *update.Client, enabled bool, recordPath, exe string) check` in `cmd/rootout/doctor.go`; the `RunE` builds the client with `update.New(version)`, the record path with `update.Path(env, os.Getenv)`, the exe with `os.Executable` + `EvalSymlinks`, and `enabled` from `loadConfig()` (that call happens twice per doctor run, once here and once inside `runChecks`; it reads one small file, so leave `runChecks` untouched). `config.Load` returns a zero `Config{}` on error (verified in `internal/config/config.go`), so `enabled` is `c.Updates.Check` when the load succeeds and `config.Default().Updates.Check` otherwise: a corrupt config must not read as "off". Cases:

| Situation | OK | Detail | Fix |
|---|---|---|---|
| `updates.check=false` | true | `off` | `rootout config set updates.check true` (shown only when OK=false, so put it in Detail: `off (rootout config set updates.check true)`) |
| `Installed(version)` fails | true | `dev build; cannot compare with the latest release` | |
| request fails | true | `could not reach GitHub: <err>` | |
| latest newer | false | `v0.8.0 available (installed v0.7.0, released 2026-11-05)` | upgrade hint |
| up to date | true | `up to date (latest v0.7.0)` | |

After a successful fetch, `update.WriteRecord` so the home screen reuses it. The helper `upgradeHint(exe string) string` lives in `cmd/rootout/doctor.go` with a unit test for the Caskroom and non-Caskroom paths (pure function; pass the path in). Update the command's `Long` text: doctor still only reads locally, but now also "asks GitHub for the latest release (off with updates.check=false)".

Add `cmd/rootout/doctor_test.go` for `upgradeHint` and for the row builder with an `httptest` server (pass the `*update.Client` in so the test sets `URL`), using `t.Setenv("ROOTOUT_CONFIG", …)` and `XDG_CACHE_HOME` pointed at `t.TempDir()` as `assess_test.go` does.

### 4. Home-screen notice

`internal/tui/home.go`:
- `PickAction(ctx, version string, flash Flash, ai string, notice func() string)` and `NewHome(version, flash, ai string, notice func() string)`. `notice` may be nil.
- `Home` gains `notice func() string` and `noticeText string`. `Init` returns nil when `notice` is nil, else a `tea.Cmd` that calls it and returns `noticeMsg(string)`; `Update` stores it. The closure already carries its own context (built in `cmd/`), so the TUI never sees `context`, config, files or HTTP, keeping D-034's "the TUI never persists" and the `cmd/`-only wiring rule.
- Render: when `noticeText != ""`, the title line becomes `rootout v0.7.0` followed by two spaces and the notice in `styles.dim`. Truncate the notice to `width - lipgloss.Width(title) - 2` and drop it when that is under 4, so a narrow terminal never wraps the title and breaks the fixed-height layout `TestHomeView` pins at 30 lines. No new chrome row, so `homeChromeRows` stays as it is.
- Tests in `home_test.go`: nil notice gives a nil `Init`; a stub notice returning text renders it on the first line after the version; an empty string renders nothing; the title still passes `TestHomeTitleVersion`. Update every `NewHome(`/`PickAction(` call site (`home_test.go` helper, `cmd/rootout/tui.go`).

`cmd/rootout/tui.go`:
- Before the loop, build `notice := updateNotice(ctx, env)` once. It returns a `func() string` that memoises its result (`sync.Once`), so each pass through the home screen reuses the first answer. Inside: load config; return "" when `!c.Updates.Check` or `Installed(version)` fails; `ReadRecord(path, now)`; when stale, `Latest` with a 5 s timeout derived from `ctx`, then `WriteRecord` (debug-log a failure); return `"v0.8.0 available · " + upgradeHint(exe)` when `Newer`, else "". Any error returns "" (stderr stays clean on the home screen; `--verbose` logging via `slog.Debug`).
- Pass `notice` to `tui.PickAction`.
- Build `notice` after the first-launch `runSetup` block, so the setup screen is never delayed and a fresh config is what the check reads.
- Make `updateNotice` testable by taking its dependencies (`*update.Client`, record path, enabled bool, version, exe, `now`) as parameters and having the production caller build them. Tests in `cmd/rootout/tui_test.go` with an `httptest` server that counts hits, `XDG_CACHE_HOME` and `ROOTOUT_CONFIG` on `t.TempDir()`: fresh record → 0 hits and the notice comes from the record; stale record → exactly 1 hit and the record rewritten with the new tag and time; calling the returned func twice → still 1 hit (memoisation); `updates.check=false` → 0 hits and ""; version `dev` → 0 hits and ""; server down → "", no panic, and a record with empty `latest` written; a fresh record with empty `latest` → 0 hits and "".
- Notice text: `v0.8.0 available · brew update && brew upgrade --cask rootout` for a Caskroom install, else `v0.8.0 available · github.com/shiva2021/rootout/releases` (short form, so it survives an 80-column title line; the doctor row carries the full URL).

### 5. Docs

- `CHANGELOG.md` Unreleased, Added: doctor "Update" row and the home-screen notice, `updates.check`, the record file path, `dev` builds never check.
- `docs/DECISIONS.md`: D-039 with the decisions above (context, decision, alternatives considered: HEAD on `github.com/…/releases/latest` and reading `Location` (no JSON, but no `published_at` and opaque to test); checking on every subcommand (rejected: scripts and `--json` users should not see it); env var instead of config key (rejected: the user asked for a config key, and `config show` should list it); `x/mod/semver` (rejected: three ints, stdlib first); consequences incl. the downgrade note and the network-policy statement).
- `README.md`: one sentence under Install ("rootout tells you when a newer release exists: `rootout doctor` checks, the home screen shows a one-line notice at most once a day; `rootout config set updates.check false` turns it off.").
- `docs/kb/architecture.md` if it lists packages: add `update`. `docs/kb/conventions.md` "Releases and versions": one line that the version string also feeds the update check, so `dev` builds never check.
- `docs/HANDOVER.md` at session end, as CLAUDE.md requires.

### 6. Branch and PR

Branch `feat/update-check` off `main` (no direct commits to main). Commits per logical step: `update: fetch latest release and compare versions`, `config: updates.check key`, `doctor: Update row`, `tui: home-screen update notice`, `docs: D-039, changelog, README`. One PR. This is a feature, so the next release is `v0.8.0`; cutting it is a separate decision (current practice: a `release/0.8.0` branch, `make release-check`).

## Verification

```bash
make build && make test && make lint          # gofmt -l . must print nothing
./bin/rootout doctor                          # second row "Update": installed is v0.7.0-N-g…, latest v0.7.0 → "up to date"
./bin/rootout doctor --json | jq '.[1]'       # {name:"Update", ok:true, detail:…}
./bin/rootout config set updates.check false && ./bin/rootout doctor | sed -n 2p   # "off (…)"
./bin/rootout config set updates.check true
XDG_CACHE_HOME=$(mktemp -d) ./bin/rootout doctor; cat "$XDG_CACHE_HOME"/rootout/update.json   # record written
go build -ldflags "-X main.version=v0.1.0" -o /tmp/rootout-old ./cmd/rootout && /tmp/rootout-old doctor   # "!" row with the brew/URL fix
/tmp/rootout-old                               # home title carries "v0.7.0 available · …" once the fetch lands; esc/ctrl+x
ROOTOUT_CONFIG=/nonexistent ./bin/rootout doctor   # still works with defaults
# offline: turn Wi-Fi off, `rootout doctor` → "could not reach GitHub" with OK=true; home screen shows no notice and does not stall
```

Tests never touch the real home: every record test goes through `testutil.FakeHome` + `RequireSandbox`, and cmd tests set `XDG_CACHE_HOME` and `ROOTOUT_CONFIG` to temp dirs.

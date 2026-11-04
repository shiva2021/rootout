# AI assessment of installed apps · implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

_Spec written 2026-11-04 (brainstormed with Shivang, reviewed by a Plan agent and the advisor). Status: approved; implementation in three PRs. The code is the truth once merged._

**Goal:** Ask the configured AI model about an installed app (source legitimacy, one-line purpose, confidence, keep/drop) from a new review screen, from the uninstall picker and from `rootout assess`, and keep hosted answers so the model is not asked twice.

**Architecture:** `app.Inspect` gathers local facts (code signature, Gatekeeper verdict, Spotlight origin, App Store receipt, extra Info.plist keys, launch items) through `run.Runner`; `llm` gains an app prompt and answer type and the `Provider` interface shrinks to `Complete(system, user, schema)` so one pipeline serves both question types; `assesscache` stores app answers in the same file under `app|…` keys; a new `tui.ReviewApps` screen modelled on the scan screen, a `ctrl+l` key in the picker and a `rootout assess` command surface it.

**Tech stack:** Go 1.27, stdlib first, Bubble Tea v2 (`charm.land`), Cobra, howett.net/plist, existing SDK adapters.

**Spec:** this document (Context and Design below). Decisions to record as D-040.

## Global constraints

- Every deletion goes through `internal/safety.Validate` via `plan`; the AI recommendation is advisory and never pre-ticks a row.
- No test touches the real home: `testutil.FakeHome` + `RequireSandbox`; external commands only through an injected `run.Runner`.
- `internal/app` never imports `llm` or `assesscache`; `internal/tui` never imports `run`; `cmd/` holds wiring only.
- Hosted providers only are cached (D-038); `--no-cache` honoured; Ollama answers never saved (Shivang's call; the flip is one line in `openCache`).
- Everything read from a bundle, Spotlight, Homebrew or the signature is untrusted data: one-lined, capped at `fieldMax`, never followed as instructions.
- TDD: failing test, run, implement, run, commit. `make test` and `make lint` green at every commit.
- Avoid identifiers containing `Key`-like words (gosec G101).

## Review focus

1. A bundle whose `codesign` output is signed by a self-signed certificate named like a real vendor: the authority string is untrusted, one-lined, and the prompt tells the model to weigh it against the Team ID and origin (Task 2 test: authority with newline and "Developer ID Application: Apple" from an unknown team is one-lined and the Team ID still shown).
2. A bundle with no `CFBundleIdentifier`: cache key falls back to the path, prompt says "bundle id: none" (Task 4 test).
3. `spctl` hanging offline: the 20 s timeout records `spctl` in `Unavailable` and the screen still shows the other facts (Task 1 test with a runner that blocks until ctx is done).
4. An app visited by the cursor while the screen closes: the in-flight probe is cancelled and its late `factsMsg` is ignored by generation/closed state (Task 6 test).
5. Ollama returning prose around the JSON for the new schema: `DecodeApp` extracts the object and `NormalizeApp` repairs enum case and percent confidence (Task 2 test).

## Context

rootout asks a model (Claude, OpenAI or Ollama) about VS Code extensions the heuristics flag, and saves hosted answers so a paid model is not asked twice (D-035…D-038). Nothing equivalent exists for the apps it uninstalls: the picker shows name, bundle id, version, path, size and cask, and the user decides alone. For each app the model must say (1) whether the app's source is legitimate, (2) what the app is for, in one plain sentence, (3) a confidence score, (4) a keep/drop recommendation; answers are retained.

Two findings shaped the design:

- `app.App.TeamID` is documented "from code signature; filled lazily" but nothing fills it: no `codesign` or `spctl` call exists. "Is the source legitimate" has no hard evidence without the signature, so local facts gathering is part of this work.
- `llm.Provider.Assess(ctx, heur.Report)` and everything around it is extension-shaped, and the three adapters each repeat build-prompt → transport → decode → normalise. A second question type is cheapest if the adapters shrink to transport.

Decisions with Shivang: entry points are **both** a review screen (home action + `rootout assess`) **and** an ask key in the uninstall picker; the cache stays **hosted-only**. Calls made in this plan: recommendation is `keep | drop | review` (`review` = the model cannot settle it, the twin of `unknown`), subcommand `assess`, picker key `ctrl+l`, facts gathered lazily, queue machinery duplicated rather than extracted, no typed "delete" box on the review screen (the per-app plan confirm that follows is the real gate), `assess <app>` without a provider prints the facts and exits 0.

## Design

```
app.Inspect(ctx, env, App, run.Runner) → app.Facts        (codesign, spctl, mdls, receipt, plist, launch items)
llm.BuildAppPrompt(Facts) → llm.AssessApp(ctx, p, Facts) → llm.AppAssessment{Purpose, Source, Confidence, Recommendation, Reasoning}
assesscache: key "app|<bundleid>|<version>|<provider>|<model>|a<AppPromptVersion>|<fp>", same file, additive
tui.ReviewApps (new screen, modelled on ScanView) · Picker ctrl+l · home row "Assess apps" · `rootout assess [app] [--all]`
```

Import graph after: `app → fsx, progress, run` · `llm → heur, app` · `assesscache → llm, app` · `tui → app, llm, plan` · `cmd → all`.

**Network note (for D-040).** Facts come from local commands, but `spctl --assess` may contact Apple's notary service. The review screen inspects a row when the cursor rests on it (two in flight, 20 s timeout, cancelled on exit), so arrowing down the list can contact Apple; accepted because the user opened a screen whose purpose is inspecting apps (D-039's concern was silent calls at launch). Eager inspection of every app at open is rejected (offline Macs would stall). Running `spctl` only in the ask path was rejected because the fingerprint would then differ between cursor and ask and saved answers could not fill in on cursor. App metadata, signature facts and download URLs reach a hosted provider only on `l`/`L`/`ctrl+l` or `rootout assess`.

---

## PR 1 · facts, answer type, provider cut, cache, wiring (no UI)

### Task 1: `app.Inspect` facts gathering

**Files:** create `internal/app/inspect.go`, `internal/app/signature.go`, `internal/app/inspect_test.go`, `internal/app/testdata/{codesign_devid,codesign_adhoc,codesign_unsigned,spctl_notarized,spctl_rejected,mdls_full,mdls_null}.txt`; modify `internal/app/plist.go` (extra keys).

**Interfaces, produces:**
```go
type Facts struct {
    App         App
    Signature   Signature   // Signed, AdHoc bool; Identifier, TeamID string; Authority []string; Timestamp string
    Gatekeeper  Gatekeeper  // Accepted bool; Source string
    Origin      Origin      // WhereFroms []string; DateAdded, LastUsed time.Time
    AppStore    bool
    Plist       PlistFacts  // Copyright, MinSystem, Executable string; UIElement, BackgroundOnly bool; URLSchemes, UsageKeys, PrivilegedHelpers []string
    LaunchItems []string
    Unavailable []string    // "codesign" | "spctl" | "mdls" | "plist"
}
func Inspect(ctx context.Context, env fsx.Env, a App, r run.Runner) Facts
```
Timeouts: `codesignTimeout = 10 * time.Second`, `spctlTimeout = 20 * time.Second`, `mdlsTimeout = 5 * time.Second`. Commands: `codesign -dv --verbose=4 <p>`, `spctl --assess --type execute -vv <p>`, `mdls -name kMDItemWhereFroms -name kMDItemDateAdded -name kMDItemLastUsedDate <p>` with `p = filepath.EvalSymlinks(a.Path)` (fallback `a.Path`). Output is parsed even when `err != nil` (codesign exit 1 "not signed at all", spctl exit 3 "rejected"); empty output or a ctx deadline → the probe name in `Unavailable`. `run.Default` uses `CombinedOutput`, which is required because codesign writes to stderr (comment it). `mdls` `(null)` → zero; `kMDItemWhereFroms = (\n "a",\n "b"\n)` parsed into the slice. Launch items: `os.ReadDir` on `env.User("Library","LaunchAgents")`, `env.Sys("Library","LaunchAgents")`, `env.Sys("Library","LaunchDaemons")`, keep `.plist` names with `strings.HasPrefix(name, a.BundleID)` (skip when BundleID is empty). `Facts.App.TeamID` is set from the signature. `Inspect` never returns an error.

- [ ] Write `inspect_test.go`: a fake runner `fakeRun(t, outputs map[string]string, errs map[string]error)` keyed by command name recording args; table cases: `devid` (Signed, TeamID `EQHXZ8M8AV`, Authority[0] `Developer ID Application: Google LLC (EQHXZ8M8AV)`, Gatekeeper accepted + `Notarized Developer ID`, Origin from `mdls_full`), `adhoc` (Signed, AdHoc, TeamID ""), `unsigned` (codesign err exit 1 with "not signed at all" → Signed false; spctl err exit 3 with `rejected`/`source=no usable signature` → Accepted false, Source set), `runner error empty output` → `Unavailable` has `codesign`, `blocking spctl` (runner waits on ctx.Done) → `spctl` unavailable within the timeout (use a short timeout via an unexported var), `symlinked bundle` (Symlinks in the spec; runner sees the resolved path), `receipt` → AppStore, launch agent `com.foo.bar.helper.plist` matched and `com.foo.bar2.plist` matched too (prefix rule, documented), custom Info.plist with `NSCameraUsageDescription`, `CFBundleURLTypes`, `SMPrivilegedExecutables`, `LSUIElement`.
- [ ] Run `go test ./internal/app -run Inspect`; expect compile failure.
- [ ] Implement `signature.go` (`parseCodesign(out []byte) Signature`, `parseSpctl(out []byte) Gatekeeper`, `parseMdls(out []byte) Origin`) and `inspect.go` (`Inspect`, launch items, receipt, plist extras via `plist.Unmarshal` into `map[string]any`).
- [ ] Run the tests; pass. `make lint`.
- [ ] Commit `app: Inspect gathers signature, Gatekeeper, origin and plist facts`.

### Task 2: `llm.AppAssessment`, schema, prompt, decode, normalise

**Files:** create `internal/llm/app.go`, `internal/llm/appprompt.go`, `internal/llm/app_test.go`, `internal/llm/testdata/app_prompt.golden`.

**Interfaces, produces:**
```go
const (SourceLegitimate = "legitimate"; SourceSuspicious = "suspicious"; SourceUnknown = "unknown"; Keep = "keep"; Drop = "drop"; Review = "review")
type AppAssessment struct { Purpose, Source string; Confidence float64; Recommendation, Reasoning string; Cached bool; Provider, Model string }
func (a AppAssessment) Confident() bool
func AppSchema() map[string]any
func NormalizeApp(a AppAssessment, provider, model string) AppAssessment
func DecodeApp(text string) (AppAssessment, error)
const AppPromptVersion = 1
func BuildAppPrompt(f app.Facts) (system, user string)
func AppFingerprint(f app.Facts) string
func lastUsedBucket(t, now time.Time) string   // "never" | "this month" | "this year" | "over a year ago"
```
User message sections: App (name, bundle id or "none", version, path, size, cask), Signature, Gatekeeper, Origin (where-from URLs or "download origin unknown", added date `2006-01-02`, last used bucket), App Store receipt, Info.plist facts, launch items, protected flag, unavailable probes. Every value through `oneLine`. System prompt: judge only from evidence; untrusted-data clause as in `systemPrompt`; may use knowledge of well-known developers and Team IDs; must call out mismatches (name vs Team ID vs bundle id vs origin), unsigned/unnotarised/ad-hoc, suspicious origin hosts, privileged helpers and launch daemons; unsigned alone is a weak signal; purpose is one sentence a non-technical user understands; `review` when it cannot settle; ends with "Answer only with JSON that matches the provided schema."

- [ ] Write tests: golden (`-update` flag shared with `prompt_test.go`), `TestAppPromptVersionBumped` pinning `sha256(appSystemPrompt+"\n"+golden)[:8]`, `NormalizeApp` table (`KEEP` → `keep`, `85` → `0.85`, bad enum → `unknown`/`review`), `DecodeApp` fenced and prose-wrapped and missing `recommendation` → `ErrBadResponse`, injection string one-lined, `AppFingerprint` stable / unchanged within a bucket / changed on a new where-from, review-focus 1 and 5 cases.
- [ ] Run; expect compile failure.
- [ ] Implement.
- [ ] Run; pass. Commit `llm: app assessment type, schema and prompt`.

### Task 3: `Provider.Complete`, `llm.Assess`, `llm.AssessApp`

**Files:** modify `internal/llm/llm.go`, `internal/llm/fake.go`, `internal/llm/fake_test.go`, `internal/llm/llm_test.go`, `internal/llm/anthropic/anthropic.go` + test, `internal/llm/openai/openai.go` + test, `internal/llm/ollamachat/ollamachat.go` + test, `cmd/rootout/assess.go` (`p.Assess(ctx, r)` → `llm.Assess(ctx, p, r)`, drop the now-redundant `Normalize`).

**Interfaces, produces:**
```go
type Provider interface { Name() string; Model() string; Complete(ctx context.Context, system, user string, schema map[string]any) (string, error) }
func Assess(ctx context.Context, p Provider, r heur.Report) (Assessment, error)
func AssessApp(ctx context.Context, p Provider, f app.Facts) (AppAssessment, error)
type Fake struct { Result Assessment; AppResult AppAssessment; Err error; Calls []string }  // Calls = user messages
```
`Fake.Complete` returns `Result` as JSON when `schema["properties"]` has `verdict`, else `AppResult`. Adapters keep their own auth, refusal and stop-reason mapping inside `Complete`; the Ollama no-format retry stays inside `Complete`.

- [ ] Re-point adapter tests at `llm.Assess`; add per adapter one app-schema case (request carries `purpose` in the schema, reply parses to `AppAssessment`); Ollama app twin of `TestAssessFallsBackWithoutFormat`; `TestFake` both kinds; `llm_test.go` error wrapping `"<name>: …"`.
- [ ] Run; fail.
- [ ] Implement the cut.
- [ ] `make test && make lint`; pass. Commit `llm: Provider.Complete; Assess and AssessApp share one pipeline`.

### Task 4: `assesscache` app entries

**Files:** modify `internal/assesscache/assesscache.go`, `internal/assesscache/assesscache_test.go`.

**Interfaces, produces:**
```go
type Entry struct { Assessment *llm.Assessment `json:"assessment,omitempty"`; App *llm.AppAssessment `json:"app,omitempty"`; At time.Time `json:"at"` }
type AppEntryID struct { BundleID, Version, Provider, Model string; PromptVersion int; Fingerprint string }
func IDForApp(f app.Facts, provider, model string) AppEntryID   // BundleID "" → Path
func (id AppEntryID) String() string   // "app|<bundleid>|<version>|<provider>|<model>|a<N>|<fp>"
func (c *Cache) LookupApp(id AppEntryID) (llm.AppAssessment, bool)
func (c *Cache) StoreApp(id AppEntryID, a llm.AppAssessment) error
```
`fileVersion` stays 1. `Lookup` returns false on a nil `Assessment`.

- [ ] Tests: today's layout (`"assessment": {...}`) still reads; round-trip both kinds; `Len` counts both; `LookupApp` marks `Cached`; `-race` concurrent `Store`/`StoreApp`; collision guard: every id from `ext.Editors(FakeHome, ext.Options{})` differs from `"app"` and no `EntryID.String()` starts with `"app|"`; review-focus 2 (empty bundle id keys by path).
- [ ] Run; fail. Implement. Run; pass. Commit `assesscache: app entries beside extension entries`.

### Task 5: `cmd/rootout/assess.go` app wiring

**Files:** modify `cmd/rootout/assess.go`, `cmd/rootout/assess_test.go`; `internal/tui/apps.go` gets only the `AppAssessFunc`/`AppAssessor` types in this PR (the screen comes in PR 2) so `cmd/` compiles.

**Interfaces, produces:**
```go
type tui.AppAssessFunc func(ctx context.Context, f app.Facts) (llm.AppAssessment, error)
type tui.AppAssessor struct { Provider, Label string; Inspect func(ctx context.Context, a app.App) app.Facts; Assess, Reassess AppAssessFunc; Cached func(f app.Facts) (llm.AppAssessment, bool); Models *ModelDeps; HostedModels func(ctx context.Context) ([]llm.ModelInfo, error); OnModel func(ctx context.Context, provider, model string) (AppAssessor, error) }
func assessAppFunc(p llm.Provider, cache *assesscache.Cache, force bool) tui.AppAssessFunc
func cachedAppFunc(p llm.Provider, cache *assesscache.Cache) func(app.Facts) (llm.AppAssessment, bool)
func withAppCache(a tui.AppAssessor, p llm.Provider, cache *assesscache.Cache) tui.AppAssessor
func inspectFunc(env fsx.Env) func(context.Context, app.App) app.Facts
func buildAppAssessor(ctx context.Context, c config.Config, provider, model string, interactive, noCache bool) (tui.AppAssessor, error)
```
`buildAssessor` and `buildAppAssessor` share an `assessorCore` returning provider, key, `cacheFor` and model deps, so the one-cache-per-run rule and `hostedLister` live once.

- [ ] Tests: app mirrors of `TestAssessFuncCachesAnswers`, `…ForceBypassesLookupButStores`, `…WithoutCache`, `TestCachedFunc` with `Fake.AppResult`; `buildAssessor` tests still pass unchanged.
- [ ] Run; fail. Implement. `make test && make lint`; pass. Commit `cmd: app assessor wiring with the shared cache`.
- [ ] Docs for PR 1: D-040 in `docs/DECISIONS.md`, `docs/kb/assessments.md` "Assessing apps" section (Inspect, AssessApp, cache key, the version test), `docs/kb/architecture.md` lines and the import rule, CHANGELOG Unreleased "Changed: `llm.Provider` is `Complete`…". Commit `docs: D-040 app assessments`.

## PR 2 · review screen, CLI, home row

### Task 6: `tui.ReviewApps`

**Files:** create `internal/tui/apps.go` (extend), `internal/tui/apps_test.go`. Reuse from `scan.go` unexported: `scanStyles`, icon constants, `iconW`, `savedTag`, `assessWorkers`, `labelSep`, `fitFooter`, `renderHelp`, `checkbox`, `padWith`, `wrapLines`, `firstSentence`, `orDash`, `embeddedPicker` hosting.

**Interfaces, produces:** `type AppsResult struct{ Uninstall []app.App }`; `func ReviewApps(ctx context.Context, apps []app.App, a AppAssessor) (AppsResult, bool, error)`; `AppsView` with `facts map[int]app.Facts`, `inspecting map[int]bool`, `results map[int]llm.AppAssessment`, `errs`, `pending`, `queue`, `force`, `gen`; messages `factsMsg{app, gen int; f app.Facts}`, `appAssessMsg{app, gen int; f app.Facts; a llm.AppAssessment; err error}`, `appAssessorMsg`. `inspectWorkers = 2`.

Behaviour: rows `marker checkbox name  version [brew|app store|system]  size icon`, drop order on narrow panes tag → version → name truncation; protected rows dim, `space` on them flashes, `ctrl+a` ticks all non-protected (unticks all when all ticked); icon ✓ keep+confident, ✗ drop+confident, ⚠ other answer, `!` error, spinner pending; legend "✓ keep ✗ drop ⚠ unsure" once anything is assessed; AI line says "none · rootout setup" without a provider. Facts: cursor trigger (bound `inspectWorkers`, re-checked after every key and `factsMsg`) and ask trigger (inspect if unknown then assess). `factsMsg` runs `Cached(facts)`. Keys esc/q, tab, up/down/pgup/pgdn, space, ctrl+a, l, L, m, ?, enter (returns ticked; nothing ticked flashes). Details pane: title, "inspecting…", AI box or error, then facts lines. Summary pane when >1 ticked and one asked. `appAssessorMsg`: `gen++`, drop `Cached` results, keep facts, re-run `Cached`. `ReviewApps` cancels ctx on exit.

- [ ] 6a tests: list/tick/ctrl+a/protected/enter; `TestAppsViewRowsExactWidth` at 40/80/140 with and without icons, brew row, protected row, long name. Run; fail; implement; pass; commit `tui: apps review screen list and ticks`.
- [ ] 6b tests: facts on cursor (recorder; bound of 2; skipped row inspected when the cursor rests), ask queue order and bound, skips answered, retries errors, `L` forces, cached fill on `factsMsg`, summary pane, model swap keeps facts, stale `gen`, review-focus 4 (late `factsMsg` after close ignored). Commit `tui: apps screen facts and ask queue`.
- [ ] 6c tests: `m` hosted/ollama picker, `?` lists every key, footer fits, ctrl+x. Commit `tui: apps screen model picker and help`.

### Task 7: `rootout assess`, home row, uninstall wiring

**Files:** create `cmd/rootout/assessapps.go` + `assessapps_test.go`; modify `cmd/rootout/main.go`, `cmd/rootout/tui.go`, `cmd/rootout/remove.go` (extract `resolveApp(apps []app.App, arg string) (app.App, error)`), `cmd/rootout/guide.go`, `internal/tui/home.go` + `home_test.go`.

**Interfaces, produces:** `type assessItem struct{ App app.App; Facts app.Facts; Assessment *llm.AppAssessment; AssessErr string }` (JSON `app, facts, assessment, assessment_error`); `func newAssessCmd(g *globalFlags) *cobra.Command` with `[app]`, `--all`, `--no-tui`, `--provider`, `--model`, `--no-cache`, `--pick-model`; `homeAssess(ctx, env) (tui.Flash, error)`; `uninstallOne(ctx, env, a app.App, rep progress.Reporter) (tui.Flash, error)` shared with `homeUninstall`; `tui.ActionAssess` after `ActionScan`, row "Assess apps · ask the AI what each installed app is and whether its source is legitimate" before "Set up AI".

Behaviour: `[app]` → resolve, inspect, assess (cache first, stderr `using the saved assessment`), print facts + answer or one JSON object; no provider → facts, stderr hint, exit 0, no `assessment` field. No arg + terminal + no `--all` → `ReviewApps`, then `uninstallOne` per ticked app, aggregate flash "Removed 2 of 3 apps · 1 cancelled". No arg + non-terminal/`--no-tui` without `--all` → exit 2 "name an app or pass --all". `--all` → every non-protected app sequentially, table `app · version · source · ai · purpose` then per-app detail, or JSON rows. `Long` carries the disclosure sentence. `runAction` gains `ActionAssess` and the AI-line refresh includes it. `guide.go` gains a step naming `rootout assess "<app>"`.

- [ ] Tests: `assessItem` JSON shape; `--all` with `llm.Fake` and a FakeHome of two apps and an injected inspect func; no-provider path exit 0; exit 2 paths; aggregate flash table; `TestHomeEnterSelects` indexes; `resolveApp` covered by `remove` tests. Run; fail; implement; pass. Commit `cmd: rootout assess and the Assess apps home action`.
- [ ] Docs: CHANGELOG Added lines, README feature bullet + command, `docs/kb/assessments.md` screen section. Commit.

## PR 3 · picker key

### Task 8: `ctrl+l` in the uninstall picker

**Files:** modify `internal/tui/picker.go`, `internal/tui/picker_test.go`, `cmd/rootout/tui.go` (`homeUninstall` passes the assessor, built once per action).

**Interfaces:** `func PickAppLoad(ctx context.Context, load LoadFunc, a *AppAssessor) (app.App, bool, error)`; `Picker.assessor *AppAssessor` (nil hides the key, footer entry and `?` row); state keyed by `Path`; `pickerAssessMsg{path string; f app.Facts; a llm.AppAssessment; err error}`.

Behaviour: `ctrl+l` handled before the textinput; no row / loading / protected → flash; answered → no-op; else one Cmd inspect → cached? → assess. `renderPreview` appends spinner "asking the AI…", or red error, or the AI block (purpose, source, `KEEP 85% · model · saved`, wrapped reasoning). Under `minPreviewCols` the purpose + recommendation go on the flash line. `helpText`/`pickerKeys` conditional on the assessor.

- [ ] Tests: key ignored when nil; `ctrl+l` with filter text asks and leaves the text; protected flashes; spinner then answer with `saved`; error line; narrow terminal uses the flash. Run; fail; implement; pass. Commit `tui: ctrl+l asks the AI about the highlighted app`.
- [ ] Docs: CHANGELOG line, `docs/kb/assessments.md`, HANDOVER. Commit.

## Verification

```bash
make build && make test && make lint          # every step
go test ./internal/llm -run PromptVersion     # pins both prompts
./bin/rootout assess Spotify --no-tui         # one app, facts + answer (real key or Ollama)
./bin/rootout assess Spotify --json | jq .
./bin/rootout assess Spotify --no-tui         # second run prints "using the saved assessment"
./bin/rootout config show                     # cache count includes the app entry
./bin/rootout                                 # home → Assess apps → cursor fills facts → space, l → icons, summary → m → L
./bin/rootout                                 # home → Uninstall an app → ctrl+l on a row → answer in the preview
./bin/rootout assess --no-tui                 # exit 2 "name an app or pass --all"
rootout config set llm.provider "" && ./bin/rootout assess Spotify --no-tui   # facts only, setup hint on stderr, exit 0
```

A pty-driven check of both screens (HANDOVER gotchas: wait for the alternate screen, then send keys) covers what unit tests cannot.

## Risks

- Slow `spctl`: 20 s timeout, on demand only, ctx cancelled when the screen closes; "unavailable" is a fact the prompt names.
- `mdls` on never-indexed apps: zero Origin; the prompt says "download origin unknown".
- Unsigned apps: non-zero codesign/spctl exits are parsed, not failures.
- Symlinked bundles: `EvalSymlinks` before probes; the key uses the bundle id.
- Prompt injection via bundle contents: every value passes `oneLine`; usage-description keys are sent as names only; no file contents are ever sent.
- Cache churn: last-used is bucketed; a raw timestamp would miss the cache on every launch.
- Full Disk Access: none of the probes need it.

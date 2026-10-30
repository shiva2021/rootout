# M6 · Membership gate

_Spec written 2026-10-30 (brainstormed with Shivang; builder-checked). Status: approved, not started._ Decisions D-033 and D-034 are logged when Task 7 lands. Implementation plans: Part A below (Go CLI, admin tool, docs, release), `m6-service.md` (Cloudflare Worker), Part C below (public flip).

## Goal

Shivang (admin and creator) uses every feature of rootout unrestricted and offline. Everyone else gets a 7-day trial of full access on each Mac, then read-only use for free and deletion plus AI assessment only with an active monthly Patreon membership. A patron pastes one licence key per Mac; the key renews itself quietly while the membership is active. Once the gate ships, the binary moves to a public releases repo and public tap so `brew install --cask rootout` needs no token (recipe agreed 2026-10-22, D-033).

Out of scope: signing and notarisation (D-030), Patreon tiers with different entitlements, Windows or Linux, a web dashboard beyond the key page, defending against clock rollback or a patched binary.

## Choices Shivang made (2026-10-30)

- Patron flow: **paste a licence key** once; works offline afterwards.
- Anti-goals, all equal: a paying patron locked out; non-payers using it freely; admin access ever depending on anything.
- Key issuance: **self-service page** on a Cloudflare Worker (TypeScript): Patreon login → key. The same service renews keys.
- Scope: **7-day trial of full access**, then `list`, `doctor`, `guide`, every `--dry-run` and `scan` on local heuristics stay free; `remove`, `orphans`, `scan --uninstall`, the review-screen uninstall and every LLM assessment need a key. Model install/uninstall is never gated.
- **Machine-bound keys**: the service remembers hashed machine ids so a trial is once per Mac; patron keys are bound too, so a shared key is useless elsewhere.
- Patreon: **any paid tier**; campaign not created yet.
- Device cap: **2 Macs per Patreon account, oldest replaced**.
- Hostname: **workers.dev subdomain** (`rootout-keys.<account>.workers.dev`); no Cloudflare zone, so alerts go to a GitHub issue.

## Research that shaped the design (Patreon API v2, docs read 2026-10-30)

- Token exchange and refresh need the client secret "on your server"; no public-client or PKCE flow is documented. A backend is required regardless.
- `GET /api/oauth2/v2/identity?include=memberships` **without** the `identity.memberships` scope returns only the membership to our own campaign, so scope `identity` suffices.
- A Free tier exists on every campaign and cannot be disabled: a member record proves nothing. Active means `patron_status == "active_patron"` and `currently_entitled_amount_cents > 0` (custom-amount pledges can have empty `currently_entitled_tiers`). Gifted memberships may be missing from `identity`; `GET /api/oauth2/v2/members/{id}` with the **creator token** is authoritative and also serves renewals.
- Creator access tokens expire (~31 days) and refresh with `grant_type=refresh_token`. Rate limits: 100 req / 2 s per client, 100 / min per token.
- Precedents: Hachidori (Patreon licence portal, 48 h offline grace); CompassDocs (14-day grace on signed tokens). No open-source CLI gating on Patreon exists.

## Design

### Key format (shared by Go and the Worker)

`ro1.<base64url(payload)>.<base64url(signature)>`, both segments unpadded. Payload is compact JSON with keys in this order:

```json
{"v":1,"kind":"patron","sub":"12345678","mid":"3f2a…(32 hex)","iat":1730246400,"exp":1733270400}
```

- `kind` ∈ `patron | trial | admin`. `sub` is the Patreon user id, `"trial"` for trial keys, `"admin"` for admin keys. `exp` is omitted for admin keys. Patron keys: `exp = iat + 35 days`; trial: `iat + 7 days`.
- Ed25519 signature over the exact payload bytes. The public key is `license.PublicKeyHex` (Go const). One keypair: the private key lives in the Worker secret `SIGNING_KEY` and in Shivang's local `~/.config/rootout-keys/private.pem`.
- `mid` = first 32 hex chars of SHA-256 of `"rootout:" + IOPlatformUUID`, read from `ioreg -rd1 -c IOPlatformExpertDevice` through `run.Runner`. **Every kind carries `mid`, admin included**: a leaked admin key is useless off Shivang's Macs; he mints one per Mac, offline, so D-024 holds.

### State machine (`internal/license.Resolve`)

1. Read the key file (`$ROOTOUT_LICENSE_FILE` or `~/.config/rootout/license`, mode 0600). Bad signature, wrong `mid`, unknown `v` or `kind` → `StatusInvalid`, denied, reason names the problem and the fix (`rootout license remove`).
2. `admin` → `StatusAdmin`, allowed, no network, ever.
3. **Renew first, then evaluate** (patron only). If `exp − now < 7 days` **or the key is expired, however old**, and the last renewal attempt (timestamp file beside the key) is more than 24 h old: `POST /renew {key}`, 3 s timeout. 200 → save the new key and continue with it. 403 → remember the reason. Network error or 503 → log under `--verbose`. Renewal needs only the signature and `sub`, so a member who has been away for months is renewed, not locked out.
4. `trial` or `patron` with `now < exp` → `StatusTrial` / `StatusMember`, allowed, `DaysLeft`.
5. `patron` with `exp ≤ now < exp + 14 days` and no 403 → `StatusGrace`, allowed, warning "membership key expired N days ago and could not be renewed; check your connection". Grace means "renewal failed", never "key is old". Trial has no grace.
6. Otherwise → `StatusExpired`, denied. A 403 reason from `/renew` is appended (declined payment, membership ended, device replaced).
7. No key file → when the caller allows a trial start: `POST /trial {mid}`; 200 → save, `StatusTrial` with `Started=true`; 409 → `StatusTrialUsed`, denied; network error → `StatusNone`, denied, reason "starting the trial needs the network once". When the caller does not allow it → `StatusNone`, denied.

Every denied message carries both routes and no dark patterns:

```
rootout: deletion and AI features are for members (trial ended 2026-11-06).
  Already a member? get your key at https://rootout-keys.<account>.workers.dev/?m=<mid>
  then run: rootout license <key>
  Not yet? the same page links to the membership.
```

**The binary bakes in only the Worker origin** (`license.ServiceURL`). The Patreon page URL, campaign id and client id live in the Worker's config, so the campaign can be created after the binary is tagged and the keys page is the one link users ever see.

### Where the gate sits

`license.Check(ctx, cmd.Name())` in the Cobra root is removed; read-only commands never touch the licence path. The gate moves to the action, which **supersedes the "single call site, no restructuring at M6" consequence of D-008/D-024**: the TUI home loop runs actions in-process, and read-only must stay free. `conventions.md` exit codes gain `4 membership required`.

| Path | Where | Trial may start |
|---|---|---|
| `remove` | `cmd/rootout/remove.go` after the dry-run return, before the confirm | yes (y/n notice; `--yes` skips) |
| `orphans` | `orphans.go` after the empty-plan check, before the confirm switch | yes |
| `scan --uninstall <id>` | top of `uninstallByID` | yes |
| review-screen uninstall | `tui.ReviewScan` gets `Assessor.Gate func() error`, run on `enter` before the typed box opens; `reviewAndUninstall` checks again before `uninstallExtensions` | no |
| LLM | `assessFunc` wrapper returns `ErrMembership`; `buildAssessor` sets `Label` to "AI: members · rootout license" when not allowed | no (per row, concurrent, not destructive intent) |
| home loop | `homeUninstall` / `homeOrphans` gate before `Confirm`; denied → red flash | yes, auto-start with flash "7-day trial started" |

`Resolve` runs once per process per gated action (a small cache in `cmd/rootout/gate.go`); the scan screen may call `assessFunc` many times. `doctor` and `license status` read and verify the key but never call `/renew` or `/trial`. Under `--json` the denial goes to stderr and the exit code is 4; stdout stays JSON-only.

### CLI surface

- `rootout license` → status line, machine code, the keys URL with `?m=<mid>`; with no key it offers the trial (y/n, `--yes`).
- `rootout license <key>` → verify (signature, `mid`, expiry), save 0600, print "member until 2026-12-04" / "trial · 7 days left" / "admin".
- `rootout license status [--json]`, `rootout license remove`.
- `doctor`: a `Membership` line after `Config`: `admin` / `member · renews 2026-12-04` / `trial · 5 days left` / `!` with fix `rootout license`.
- Home title `rootout v0.5.0 · trial: 5 days left` (or `· member`, `· admin`, `· members only after trial`); gated rows' blurbs end with `(members)` when not allowed.
- `firstRunHint` skips `license`.

### Admin tooling (`cmd/rootout-keys`, never shipped)

`go run ./cmd/rootout-keys gen|admin|sign`. `gen` writes `~/.config/rootout-keys/private.pem` (0600) and prints the public key hex (for `PublicKeyHex`) and the PKCS#8 base64 (for the Worker secret). `admin --mid <code>` prints an admin key for one Mac. `sign --kind patron --sub <id> --mid <code> --days 35` is the manual fallback. GoReleaser builds only `./cmd/rootout`, so nothing changes there. **Backup of `private.pem` in Shivang's password manager is a step of `gen`'s runbook entry, not an aside**: losing it means a new public const, a new release, and every key dead.

### Service (`service/`, Cloudflare Worker, TypeScript)

Detailed in `m6-service.md`. Routes: `GET /` (key page, machine code prefilled from `?m=`), `GET /callback` (Patreon OAuth → verify active → device cap → mint), `POST /renew`, `POST /trial`, scheduled daily creator-token refresh with a GitHub-issue alert on failure. KV holds the creator token pair, trial mids, `devices:<userId>` (max 2, oldest replaced) and `member:<userId>` → member id. Stored data: hashed machine ids, Patreon user and member ids, the creator token. No names or emails.

### Public releases repo (D-033)

After v0.5.0 is cut, a **second tag v0.5.1** carries the flip (the tap's cask is only rewritten by a release). Steps in Part C.

### Also in this milestone

- **GoReleaser 2.19.0** (released 2026-10-22; "feat(cask): add hooks install_steps, deprecate raw Ruby hooks (#6873)") is stable in Homebrew. Bump first: the `postflight` deprecation warning would otherwise greet every new public user.
- `LICENSE`: short proprietary notice.
- `docs/research/vscode-extension-audit/` names a personal username: the source repo stays private, and the releases README must not link into it.

### Defaults applied (Shivang saw these)

Patron key life 35 days, silent renewal under 7 days left, 14-day grace only when renewal fails, trial 7 days with no grace. Key file rather than Keychain. Admin keys machine-bound. Trial starts from `rootout license`, from a deletion in a subcommand (y/n) or from a deletion in the TUI (auto, with a flash); never from the LLM path. Denied in the TUI is a flash, not an exit. Alerts via GitHub issue. Prose uses Australian spelling ("licence"); identifiers stay `license`.

## Verification (whole milestone)

- `make test`, `make lint`, `scripts/toolchain_check.sh` green; `cd service && npm test` green; the Go golden key verifies in the Worker's tests.
- On this Mac: `rootout license` → trial starts; `remove Spotify --dry-run` free; `rootout license remove`, reset the KV trial entry, `remove Spotify` → trial again; `rootout-keys admin` → `doctor` shows `admin`; `scan` screen shows the AI label without a key.
- A second Patreon account (or a friend) pledges, fetches a key from the page, runs `rootout license <key>`; a third Mac evicts the first.
- `make release-check NEXT=v0.5.0`, tag; Part C; a token-free `brew install --cask rootout` in a clean shell.

## Risks

- Creator token is the service's single point of failure: mitigated by atomic KV writes, `/callback` fallback to the user's own identity data, 503 on `/renew` (grace applies), GitHub-issue alert, manual re-seed in the runbook.
- Private key loss: backup step in the runbook; rotation path is a second accepted public const for one release cycle.
- Patreon field semantics (free members, gifted, custom amounts) verified only from docs and forum posts; the first real pledge is the test.
- `ioreg` output on a VM may lack `IOPlatformUUID`: `MachineID` returns an error, every gate denies with "could not read this Mac's id", nothing crashes.

---

# Part A · Go CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gate deletion and AI assessment behind machine-bound Ed25519 licence keys with a 7-day trial, an offline admin path and a `license` command, and ship it as v0.5.0.

**Architecture:** `internal/license` owns the key codec, machine id, key file, service client and the `Resolve` state machine; `cmd/rootout/gate.go` adapts it for commands and caches one resolution per process. Gates sit at the action (before any confirm), not at the Cobra root. `cmd/rootout-keys` signs keys locally with the same codec.

**Tech Stack:** Go 1.27 stdlib (`crypto/ed25519`, `encoding/base64`, `encoding/json`, `net/http`), Cobra, Bubble Tea v2; existing `run.Runner`, `fsx.Env`, `testutil.FakeHome`, `httptest`.

**Spec:** this file, sections above. Worker plan: `docs/plans/m6-service.md`.

## Global Constraints

- Go 1.27 pinned in `go.mod`; `CGO_ENABLED=0`; stdlib first, no new modules.
- Every package touching the filesystem takes `fsx.Env`; tests use `testutil.FakeHome` + `RequireSandbox`; no test touches the real home or execs a real command.
- Exit codes: 0 ok, 1 error, 2 bad input, 3 declined, **4 membership required** (`exitLicensing`, already defined in `main.go`).
- `--json`: only JSON on stdout; diagnostics and denials on stderr.
- Key file and renew-stamp written 0600, directory 0700 (gosec G306/G301).
- No `panic` outside `main`; sentinel errors + `errors.Is/As` (errorlint).
- Prose and messages in Australian spelling ("licence"); identifiers `license`.
- Each task: tests first, `make test && make lint` green, one commit, imperative subject under 72 chars, `D-034` referenced where relevant. Work on branch `m6/membership`; merge to `main` for the tag.

## Review Focus

1. A key whose `exp` is missing for `kind=patron|trial`, or `iat` in the future → `StatusInvalid`, never a panic or an infinite grace (Task 3 test `TestResolveMalformedClaims`).
2. `ioreg` output without `IOPlatformUUID` (VM, odd hardware) → `MachineID` error → every gate denies with "could not read this Mac's id" (Task 2 test `TestMachineIDMissingUUID`; Task 5 test `TestGateMachineIDError`).
3. The service returns a key that does not verify (wrong `mid`, bad signature) on `/renew` or `/trial` → treated like a network error, old key kept (Task 3 test `TestResolveRenewReturnsBadKey`).
4. `remove --json` without a key → stdout empty, stderr carries the denial, exit 4 (Task 5 test `TestRemoveJSONDenied`).
5. Many concurrent `assessFunc` calls on the scan screen → one `Resolve`, one `/trial` at most, no data race under `go test -race` (Task 5 test `TestGateCacheConcurrent`).

---

### Task 1: GoReleaser 2.19.0 and the `install_steps` hook

**Files:**
- Modify: `.github/workflows/release.yml` (`version: v2.18.3` → `v2.19.0`)
- Modify: `.goreleaser.yaml` hooks block (lines ~105-111)
- Modify: `docs/kb/setup.md` (local GoReleaser version), `docs/kb/release.md` (Known limits: remove the `postflight` deprecation note), `docs/HANDOVER.md` Next steps 5 (done)

**Interfaces:** none.

- [ ] **Step 1: Upgrade the local tool**

Run: `brew upgrade goreleaser && goreleaser --version | head -1`
Expected: `2.19.0`.

- [ ] **Step 2: Replace the hook**

In `.goreleaser.yaml` replace the `hooks.post.install` block with:

```yaml
    hooks:
      post:
        install_steps: |
          on_macos do
            run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{ .StagedPath }}/rootout"]
          end
```

Remove the "GoReleaser 2.18.3 form" comment.

- [ ] **Step 3: Pin the action**

In `.github/workflows/release.yml` set `version: v2.19.0`.

- [ ] **Step 4: Verify the rendered cask**

Run: `goreleaser check && make snapshot && grep -n -A4 'postflight_steps' dist/homebrew/Casks/rootout.rb && ruby -c dist/homebrew/Casks/rootout.rb`
Expected: a `postflight_steps do … on_macos do … run "/usr/bin/xattr" …` block; `Syntax OK`. No `postflight do` left.

- [ ] **Step 5: Docs and commit**

Update `docs/kb/setup.md` (2.19.0), `docs/kb/release.md` (drop the deprecation limit), `docs/HANDOVER.md` (Next steps 5 done; blocker line removed).

```bash
git add .goreleaser.yaml .github/workflows/release.yml docs/kb/setup.md docs/kb/release.md docs/HANDOVER.md
git commit -m "release: GoReleaser 2.19.0, cask hook via install_steps"
```

---

### Task 2: Key codec and machine id (`internal/license`)

**Files:**
- Create: `internal/license/claims.go`, `internal/license/codec.go`, `internal/license/machine.go`
- Create: `internal/license/codec_test.go`, `internal/license/machine_test.go`, `internal/license/testkeys_test.go`
- Create: `internal/license/testdata/golden.json` (generated with `-update`)
- Modify: `internal/license/license.go` (package doc only; `Check` is removed in Task 5)

**Interfaces:**
- Produces:
  ```go
  type Kind string
  const (KindPatron Kind = "patron"; KindTrial Kind = "trial"; KindAdmin Kind = "admin")
  type Claims struct {
      V int `json:"v"`; Kind Kind `json:"kind"`; Sub string `json:"sub"`
      MID string `json:"mid"`; IAT int64 `json:"iat"`; EXP int64 `json:"exp,omitempty"`
  }
  const Prefix = "ro1"
  const PublicKeyHex = "" // filled by Task 4 after `rootout-keys gen`; empty = no embedded key, Decode fails closed
  func Encode(c Claims, priv ed25519.PrivateKey) (string, error)
  func Decode(key string, pub ed25519.PublicKey) (Claims, error) // wraps ErrBadKey
  var ErrBadKey = errors.New("license: invalid key")
  func (c Claims) Expires() (time.Time, bool)  // false for admin
  func (c Claims) Valid() error                 // v==1, known kind, 32-hex mid, exp present unless admin, exp>iat
  func MachineID(ctx context.Context, r run.Runner) (string, error) // ErrNoMachineID
  var ErrNoMachineID = errors.New("license: could not read this Mac's id")
  const machineSalt = "rootout:"
  ```
- Test keypair: `testkeys_test.go` derives `testPriv, testPub` from `ed25519.NewKeyFromSeed(seed)` with `seed = sha256("rootout-test-seed")`; the Worker test uses the same seed (see `m6-service.md`).

- [ ] **Step 1: Write the failing codec tests** in `codec_test.go`

```go
func TestEncodeDecodeRoundTrip(t *testing.T) // patron claims survive; Decode with testPub returns equal Claims
func TestDecodeRejects(t *testing.T) // table: wrong prefix, 2 segments, bad base64, tampered payload byte, wrong public key, Valid() failures (v=2, kind "x", mid 10 chars, patron without exp, exp<iat) → errors.Is(err, ErrBadKey)
func TestExpiresAdminNone(t *testing.T) // admin claims → ok=false
func TestGoldenKey(t *testing.T) // decode testdata/golden.json "key" with pub from the file; with -update regenerate: {"seed_hex","pub_hex","key","claims"} for the fixed claims {1,patron,"12345678","0123456789abcdef0123456789abcdef",1730246400,1733270400}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/license/ -run 'TestEncode|TestDecode|TestExpires|TestGolden' -v`
Expected: FAIL, undefined `Encode`/`Decode`.

- [ ] **Step 3: Implement `claims.go` and `codec.go`**

Payload bytes are `json.Marshal(Claims)` (field order as declared); `Encode` joins `Prefix + "." + base64.RawURLEncoding(payload) + "." + base64.RawURLEncoding(sig)`. `Decode` splits on `.`, checks `Prefix`, decodes, verifies with `ed25519.Verify`, unmarshals, calls `Valid()`. Signature verified **before** unmarshal. Write `golden.json` with `-update` (reuse the repo's `-update` flag pattern from heur golden tests).

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/license/ -run 'TestEncode|TestDecode|TestExpires|TestGolden' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing machine id tests** in `machine_test.go`

```go
func TestMachineIDFromIoreg(t *testing.T) // fake Runner asserts argv == ["ioreg","-rd1","-c","IOPlatformExpertDevice"], returns a captured plist-ish block containing `"IOPlatformUUID" = "7A3F…-…"`; expect sha256("rootout:"+uuid) hex[:32]
func TestMachineIDMissingUUID(t *testing.T) // output without the key → errors.Is(err, ErrNoMachineID)
func TestMachineIDRunnerError(t *testing.T) // Runner error → wrapped, ErrNoMachineID
```

- [ ] **Step 6: Run to verify they fail**; **Step 7: Implement `machine.go`** (regex `"IOPlatformUUID" = "([0-9A-Fa-f-]+)"`, uppercase the UUID before hashing); **Step 8: Run to verify they pass**

Run: `go test ./internal/license/ -race -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/license
git commit -m "license: Ed25519 key codec and machine id (D-034)"
```

---

### Task 3: Key store, service client and `Resolve`

**Files:**
- Create: `internal/license/store.go`, `internal/license/client.go`, `internal/license/resolve.go`
- Create: `internal/license/store_test.go`, `internal/license/client_test.go`, `internal/license/resolve_test.go`

**Interfaces:**
- Consumes: Task 2.
- Produces:
  ```go
  func Path(env fsx.Env, getenv func(string) string) string // $ROOTOUT_LICENSE_FILE or env.User(".config","rootout","license")
  type Store struct{ Path string }
  var ErrNoKey = errors.New("license: no key installed")
  func (s Store) Load() (string, error)          // trims whitespace; ErrNoKey when absent
  func (s Store) Save(key string) error          // MkdirAll 0700, WriteFile 0600
  func (s Store) Remove() error                  // nil when absent; also removes the stamp
  func (s Store) LastRenew() (time.Time, bool)   // stamp file s.Path+".renew", RFC3339
  func (s Store) MarkRenew(t time.Time) error

  const ServiceURL = "https://rootout-keys.CHANGEME.workers.dev" // Task 7 sets the real host
  type Client struct{ BaseURL string; UserAgent string; HTTP *http.Client } // HTTP nil → &http.Client{Timeout: 3*time.Second}; UserAgent "rootout/<version>"
  type DeniedError struct{ Reason string }        // 403 from /renew; Error() = "membership check failed: "+Reason
  var ErrTrialUsed = errors.New("license: the trial was already used on this Mac")
  var ErrServiceUnavailable = errors.New("license: key service unavailable")
  func (c Client) Renew(ctx context.Context, key string) (string, error) // POST /renew {"key"}; 200 {"key"}; 403 {"reason"} → *DeniedError; 5xx/net → wraps ErrServiceUnavailable
  func (c Client) Trial(ctx context.Context, mid string) (string, error)  // POST /trial {"mid"}; 200 {"key"}; 409 → ErrTrialUsed; else ErrServiceUnavailable

  type Status string
  const (StatusAdmin Status = "admin"; StatusMember = "member"; StatusTrial = "trial"; StatusGrace = "grace"
         StatusExpired = "expired"; StatusTrialUsed = "trial-used"; StatusNone = "none"; StatusInvalid = "invalid"; StatusNoMachine = "no-machine-id")
  type Deps struct {
      Env fsx.Env; Getenv func(string) string; Run run.Runner; Now func() time.Time
      Pub ed25519.PublicKey  // nil → embedded PublicKeyHex
      Client *Client         // nil → never renews or starts a trial
      StartTrial bool        // may call /trial when no key is installed
  }
  type State struct {
      Status   Status    `json:"status"`
      Allowed  bool      `json:"allowed"`
      Kind     Kind      `json:"kind,omitempty"`
      MID      string    `json:"machine_id,omitempty"`
      Expires  time.Time `json:"expires,omitzero"`
      DaysLeft int       `json:"days_left"`
      Warning  string    `json:"warning,omitempty"`
      Reason   string    `json:"reason,omitempty"`
      Started  bool      `json:"trial_started,omitempty"` // a trial key was just issued
  }
  func Resolve(ctx context.Context, d Deps) State
  func (s State) Summary() string // "admin" | "member · renews 2026-12-04" | "trial · 5 days left" | "expired 2026-11-06" | "trial used on this Mac" | "no key" | "invalid key: …" | "could not read this Mac's id"
  const (patronLife = 35*24*time.Hour; trialLife = 7*24*time.Hour; renewAhead = 7*24*time.Hour; grace = 14*24*time.Hour; renewEvery = 24*time.Hour)
  ```

- [ ] **Step 1: Write failing store tests** (`FakeHome`; `Path` honours `ROOTOUT_LICENSE_FILE`; `Save` mode 0600 and dir 0700; `Load` on missing → `ErrNoKey`; `Remove` idempotent and clears the stamp; `LastRenew` round-trip)

- [ ] **Step 2: Run to verify they fail**; **Step 3: Implement `store.go`**; **Step 4: Run to verify they pass**

- [ ] **Step 5: Write failing client tests** with `httptest.Server`: `TestClientRenewOK`, `TestClientRenewDenied` (403 `{"reason":"declined_patron"}` → `*DeniedError{Reason:"declined_patron"}` via `errors.As`), `TestClientRenew503` (→ `ErrServiceUnavailable`), `TestClientTrialUsed` (409), `TestClientNetworkError` (closed server), `TestClientTimeout` (handler sleeps 5 s, client 100 ms → error within 1 s).

- [ ] **Step 6: Run to verify they fail**; **Step 7: Implement `client.go`** (`Content-Type: application/json`, `User-Agent: rootout/<version>` passed via `Client.UserAgent string`, response body limited to 64 KiB); **Step 8: Run to verify they pass**

- [ ] **Step 9: Write failing `Resolve` table tests** in `resolve_test.go`, each with a fake Runner returning a fixed UUID, `Now` fixed at `2026-11-01T00:00:00Z`, `Pub = testPub`, keys signed with `testPriv`, and an `httptest` service recording calls:

| name | key on disk | service | expect |
|---|---|---|---|
| `Admin` | admin, right mid | down | `StatusAdmin`, allowed, 0 calls |
| `AdminWrongMID` | admin, other mid | down | `StatusInvalid`, denied |
| `MemberFresh` | patron, exp +20d | down | `StatusMember`, DaysLeft 20, 0 calls |
| `MemberRenewsAhead` | patron, exp +3d | 200 new key exp +35d | `StatusMember`, DaysLeft 35, key file replaced, stamp written |
| `MemberRenewThrottled` | patron, exp +3d, stamp 1 h old | 200 | 0 calls, `StatusMember` |
| `ExpiredRenewOK` | patron, exp −60d | 200 | `StatusMember` (away for months, renewed) |
| `ExpiredGraceNet` | patron, exp −3d | down | `StatusGrace`, allowed, Warning non-empty |
| `ExpiredGrace503` | patron, exp −3d | 503 | `StatusGrace` |
| `ExpiredDenied403` | patron, exp −3d | 403 declined | `StatusExpired`, denied, Reason contains "declined" |
| `ExpiredPastGrace` | patron, exp −20d | down | `StatusExpired`, denied |
| `TrialActive` | trial, exp +2d | down | `StatusTrial`, DaysLeft 2 |
| `TrialExpiredNoGrace` | trial, exp −1h | down | `StatusExpired`, denied, 0 calls |
| `NoKeyStartTrial` | none | 200 trial key | `StatusTrial`, Started, file saved |
| `NoKeyTrialUsed` | none | 409 | `StatusTrialUsed`, denied |
| `NoKeyOffline` | none | down | `StatusNone`, denied, Reason mentions network |
| `NoKeyNotAllowed` | none, `StartTrial=false` | up | `StatusNone`, 0 calls |
| `InvalidSignature` | tampered | down | `StatusInvalid`, denied, Reason mentions `rootout license remove` |
| `NoMachineID` | any | down | `StatusNoMachine`, denied |

plus `TestResolveMalformedClaims` (patron key without `exp`, `iat` 2 days in the future → `StatusInvalid`) and `TestResolveRenewReturnsBadKey` (200 with a key signed by another keypair → old key kept, `StatusGrace` when expired, `StatusMember` when not).

- [ ] **Step 10: Run to verify they fail**; **Step 11: Implement `resolve.go`** following the state machine in the spec (renew before evaluate; `DaysLeft = ceil((exp−now)/24h)`); **Step 12: Run to verify they pass**

Run: `go test ./internal/license/ -race -cover`
Expected: PASS, coverage ≥ 90 % for the package.

- [ ] **Step 13: Commit**

```bash
git add internal/license
git commit -m "license: key store, service client and Resolve state machine (D-034)"
```

---

### Task 4: `rootout-keys` admin tool and the membership runbook

**Files:**
- Create: `cmd/rootout-keys/main.go`, `cmd/rootout-keys/main_test.go`
- Create: `docs/kb/membership.md`; modify `docs/kb/README.md` (index row)
- Modify: `internal/license/claims.go` → set `PublicKeyHex` to the real public key produced by `gen` (Shivang runs `gen` once; the hex goes in this commit)
- Modify: `.gitignore` (nothing: the key lives under `~/.config`, outside the repo)

**Interfaces:**
- Consumes: `license.Encode`, `license.Claims`, constants from Task 3.
- Produces (CLI, Cobra):
  - `rootout-keys gen [--out ~/.config/rootout-keys/private.pem]` → writes PKCS#8 PEM 0600 (refuses to overwrite), prints `public key hex: …` and `worker secret (PKCS8 base64): …`.
  - `rootout-keys admin --mid <32 hex> [--key path]` → prints an admin key (`sub:"admin"`, no exp).
  - `rootout-keys sign --kind patron|trial --sub <id> --mid <32 hex> --days 35 [--key path]` → prints a key.
  - `rootout-keys inspect <key>` → prints claims as JSON (verification uses the public key derived from the private key file).
  - Exit codes as the main binary; all output on stdout; errors on stderr.

- [ ] **Step 1: Write failing tests** (`main_test.go` drives `run(args, stdin, stdout, stderr) int` with `--out`/`--key` under `t.TempDir()`): `TestGenWritesPEMAndPrintsKeys` (file mode 0600, stdout has two lines with hex of 64 chars and base64), `TestGenRefusesOverwrite`, `TestAdminKeyDecodes` (decode the printed key with the public key from the PEM → `KindAdmin`, mid, no exp), `TestSignPatronDays` (exp − iat == 35 days), `TestSignRejectsBadMID`.

- [ ] **Step 2: Run to verify they fail**; **Step 3: Implement** (`x509.MarshalPKCS8PrivateKey`, `pem.Encode`; parse with `x509.ParsePKCS8PrivateKey`); **Step 4: Run to verify they pass**

Run: `go test ./cmd/rootout-keys/ -v && go vet ./...`

- [ ] **Step 5: Shivang generates the real keypair**

Run: `go run ./cmd/rootout-keys gen`
Then: paste the public hex into `license.PublicKeyHex`; store `~/.config/rootout-keys/private.pem` **and** the printed PKCS8 base64 in the password manager (the runbook names the entry "rootout signing key"). The Worker secret is set in `m6-service.md` Task S6.

- [ ] **Step 6: Write `docs/kb/membership.md`**

Sections: How keys work (format, kinds, lifetimes, machine id); Admin key per Mac (`rootout license` prints the machine code → `rootout-keys admin --mid …` → `rootout license <key>`); Key backup and rotation (second accepted const for one release cycle); Service operations (secrets list, `wrangler kv` re-seed of the creator token, reading the alert issue, resetting a trial `wrangler kv key delete trial:<mid>`, device list `devices:<userId>`); Support answers (declined payment, device replaced, lost key). Add the row to `docs/kb/README.md`: "`membership.md` | Touching the gate, issuing or rotating keys, or operating the key service".

- [ ] **Step 7: Commit**

```bash
git add cmd/rootout-keys internal/license/claims.go docs/kb/membership.md docs/kb/README.md
git commit -m "rootout-keys: local signing tool; embed the public key; membership runbook (D-034)"
```

---

### Task 5: Gate the actions, `license` command, doctor and home indicators

**Files:**
- Create: `cmd/rootout/gate.go`, `cmd/rootout/gate_test.go`, `cmd/rootout/license.go`, `cmd/rootout/license_test.go`
- Modify: `cmd/rootout/main.go` (remove the `license.Check` call at line 89; register `newLicenseCmd(&g)`; `firstRunHint` skips `"license"`), `remove.go`, `orphans.go`, `scan.go`, `assess.go`, `tui.go`, `doctor.go`
- Modify: `internal/tui/scan.go` (`Assessor.Gate`), `internal/tui/home.go` (`PickAction(ctx, version, status string, flash Flash)`, `(members)` blurbs), `internal/tui/scan_test.go`, `internal/tui/home_test.go`
- Delete: `internal/license/license.go` `Check` function (keep the package doc in `claims.go`)
- Modify: `docs/kb/conventions.md` (exit code 4), `cmd/rootout/guide.go` and root `Long` (one line each about membership)

**Interfaces:**
- Consumes: Task 3 `Resolve`, `State`, `Deps`, `Client`, `Store`, `Path`, `ServiceURL`.
- Produces:
  ```go
  // gate.go
  var ErrMembership = errors.New("membership required")
  type membershipError struct{ st license.State }  // Error() = the two-route message from the spec; Is(ErrMembership) = true
  func gateDeps(env fsx.Env, startTrial bool) license.Deps // Run: run.Default, Now: time.Now, Client: &license.Client{BaseURL: license.ServiceURL, UserAgent: "rootout/"+version}
  func gate(ctx context.Context, env fsx.Env, startTrial bool) (license.State, error) // cached per process once Allowed or denied-without-trial-possibility; a denied StatusNone with startTrial=false is re-resolved when a later call passes startTrial=true
  func denyExit(err error) error // *membershipError → &exitErr{code: exitLicensing, msg: err.Error()}
  func trialNotice(st license.State, out io.Writer) // "7-day trial started · ends 2026-11-06" when st.Started
  func licenseStatusLine(ctx context.Context, env fsx.Env) string // home title suffix: "admin" | "member" | "trial: 5 days left" | "members only after trial" | "" on StatusNone; Resolve with Client=nil, StartTrial=false
  func confirmTrial(in io.Reader, out io.Writer) bool // "Start your free 7-day trial? [y/N] " → y/Y
  // license.go
  func newLicenseCmd(g *globalFlags) *cobra.Command // Use "license [key]", MaximumNArgs(1), flags --yes; subcommands status, remove
  func licenseInstall(ctx, env, key string, out io.Writer) error // Decode with embedded pub, mid must match, save; prints Summary()
  func licenseStatus(ctx, env, jsonOut bool, out io.Writer) error // Resolve with Client=nil, StartTrial=false; --json emits State
  func licenseShow(ctx, env, yes bool, in io.Reader, out io.Writer) error // no args: status + machine code + keys URL; offers trial when StatusNone
  // tui
  type Assessor struct { Label string; Assess AssessFunc; Gate func() error; Models *ModelDeps; OnModel ... } // Gate nil = allowed
  func PickAction(ctx context.Context, version, status string, flash Flash) (Action, error)
  ```

- [ ] **Step 1: Write failing gate tests** (`gate_test.go`, `FakeHome`, `ROOTOUT_LICENSE_FILE` into the fake home, injected fake Runner and `httptest` service via a package-level `gateDepsFn` hook): `TestGateAllowedCached` (two calls, one Resolve), `TestGateCacheConcurrent` (50 goroutines, `-race`, one `/trial` call), `TestGateMachineIDError` (Runner error → `ErrMembership`, message contains "could not read this Mac's id"), `TestMembershipErrorMessage` (contains `ServiceURL + "/?m=" + mid`, "rootout license", no Patreon URL).

- [ ] **Step 2: Run to verify they fail**; **Step 3: Implement `gate.go`**; **Step 4: Run to verify they pass**

- [ ] **Step 5: Write failing gate-site tests** in the existing cmd test files (each drives the command through `runMain` with a fake home and a fake Runner, asserting exit code and stderr):
  - `TestRemoveDryRunFreeWithoutKey` (exit 0, no service call), `TestRemoveDeniedExit4` (trial used → exit 4, stderr has the two-route message), `TestRemoveJSONDenied` (stdout empty), `TestRemoveTrialPromptNo` (stdin "n" → exit 3, nothing removed), `TestRemoveTrialYesStarts` (`--yes` → `/trial` called once).
  - `TestOrphansListFreeWithoutKey`, `TestOrphansRemoveDenied`.
  - `TestScanHeuristicsFreeWithoutKey` (`scan --no-tui`, exit 0), `TestScanLLMDeniedExit4` (`--no-tui --llm` with a configured provider and no key → heuristics printed, exit 4), `TestScanUninstallDenied`.
  - `internal/tui/scan_test.go`: `TestReviewScanGateBlocksEnter` (Gate returns an error → typed box never opens, flash shows the message), `TestReviewScanGateNilAllows`.
  - `internal/tui/home_test.go`: `TestHomeTitleStatus` (title contains `· trial: 5 days left`), `TestHomeBlurbsMembers` (uninstall and orphans rows end with `(members)` when status says so).

- [ ] **Step 6: Run to verify they fail**; **Step 7: Wire the gates**
  - `remove.go`: after the dry-run return and before `yesBanner`: `st, err := gate(ctx, env, f.yes || confirmTrial(os.Stdin, os.Stderr)); if err != nil { return denyExit(err) }; trialNotice(st, os.Stderr)`. The prompt is shown only when no key exists (check `license.Store.Load` first so members never see it).
  - `orphans.go`: same, after the empty-plan check, before the confirm switch.
  - `scan.go`: `uninstallByID` top (same pattern); `reviewAndUninstall` passes `a.Gate = func() error { _, err := gate(ctx, env, false); return err }` and re-checks before `uninstallExtensions`.
  - `assess.go`: `assessFunc(p, allowed func() error)`; when it returns an error, `Assess` returns it (shows inline). `buildAssessor` resolves once (`gate(ctx, env, false)`); when denied, `Label = "AI: members · rootout license"` and `Assess` still set so the row error explains; `OnModel` keeps the same `allowed`.
  - `tui.go`: `homeUninstall`/`homeOrphans` gate with `startTrial=true` before `Confirm`; on `*membershipError` return `tui.Flash{Text: err.Error() first line, Err: true}`; on `st.Started` prepend "7-day trial started · " to the outcome flash. `runTUI` computes `status := licenseStatusLine(ctx, env)` (Resolve with `Client=nil`, `StartTrial=false`) before each `PickAction`.
  - `main.go`: remove the root `license.Check`; add `newLicenseCmd`; `firstRunHint` skip list gains `"license"`.
  - `doctor.go`: `Membership` check after `Config` using `licenseStatus` state: `OK = Allowed || Status==StatusNone`-style: `admin`/`member`/`trial` → OK; `expired`/`trial-used`/`invalid`/`no-machine-id` → `!` with Fix `rootout license`; `none` → OK with Detail "no key · 7-day trial available (rootout license)". Add to `Long`.
  - `internal/tui/home.go`: title suffix `· <status>` when non-empty; blurbs.
  - `internal/license/license.go`: delete `Check`; move the package doc.
  - `docs/kb/conventions.md`: "Exit codes: 0 ok, 1 error, 2 ambiguous/invalid input, 3 user declined, 4 membership required."
  - Root `Long` and `guide.go`: one line each: "Deletion and AI assessment need a membership after a 7-day trial; `rootout license` explains."

- [ ] **Step 8: Write failing `license` command tests** (`license_test.go`): `TestLicenseInstallOK` (key for this mid → file saved 0600, stdout "member until …"), `TestLicenseInstallWrongMID` (exit 2, message names another Mac), `TestLicenseInstallBadKey` (exit 2), `TestLicenseStatusJSON` (fields `status`, `allowed`, `expires`, `days_left`), `TestLicenseRemove`, `TestLicenseNoArgsOffersTrial` (stdin "y" → `/trial` called, "7-day trial started"), `TestLicenseNoArgsShowsURL` (contains `/?m=<mid>`).

- [ ] **Step 9: Run to verify they fail**; **Step 10: Implement `license.go`**; **Step 11: Full run**

Run: `make test && make lint && go test -race ./cmd/... ./internal/license/ ./internal/tui/`
Expected: all PASS, lint clean.

- [ ] **Step 12: Manual check on this Mac**

Run: `make build && ./bin/rootout doctor | grep Membership && ./bin/rootout license && ./bin/rootout remove Spotify --dry-run >/dev/null && echo free`
Expected: `Membership` line with "no key · 7-day trial available", the licence screen with this Mac's machine code and URL, `free`. Then `go run ./cmd/rootout-keys admin --mid <code>` → `./bin/rootout license <key>` → `doctor` shows `admin`; bare `./bin/rootout` title ends `· admin`.

- [ ] **Step 13: Commit**

```bash
git add cmd/rootout internal/tui internal/license docs/kb/conventions.md
git commit -m "gate: membership at the action; license command; doctor and home status (D-034)"
```

---

### Task 6: Service contract test against the Worker (after `m6-service.md` S1–S5)

**Files:**
- Create: `internal/license/contract_test.go` (build tag `contract`, skipped unless `ROOTOUT_SERVICE_URL` is set)

**Interfaces:** consumes `Client` from Task 3; the Worker running under `wrangler dev` with the test keypair seed (see `m6-service.md` S5).

- [ ] **Step 1: Write the test**: `TestContractTrialThenRenewDenied` — `Trial(mid)` returns a key that `Decode`s with `testPub`, `kind=trial`, `exp−iat == 7 days`; a second `Trial(mid)` → `ErrTrialUsed`; `Renew(trialKey)` → `*DeniedError` (trial keys do not renew).
- [ ] **Step 2: Run** with `cd service && npm run dev` in another shell: `ROOTOUT_SERVICE_URL=http://127.0.0.1:8787 go test -tags contract ./internal/license/ -run TestContract -v` → PASS.
- [ ] **Step 3: Commit** `git commit -m "license: contract test against the key service"`

---

### Task 7: Decisions, docs, changelog, release v0.5.0

**Files:**
- Modify: `docs/DECISIONS.md` (D-033 planned-status entry, D-034 accepted), `docs/PLAN.md` (M6 section → done summary, library table unchanged), `CHANGELOG.md` (`## [0.5.0]`), `README.md` (Membership section: trial, what is free, `rootout license`, keys page), `docs/kb/architecture.md` (license package, gate placement, service), `docs/HANDOVER.md`
- Create: `LICENSE`
- Modify: `internal/license/client.go` `ServiceURL` → the deployed workers.dev host (from `m6-service.md` S6)

- [ ] **Step 1: Write D-034** in the house style (Context, Decision, Alternatives, Consequences, Status: Accepted; supersedes the single-call-site consequence of D-008/D-024). Write **D-033** with Status "Accepted, executed in v0.5.1 (Part C)".
- [ ] **Step 2: `LICENSE`**: "Copyright © 2026 Shivang Chaturvedi. All rights reserved. rootout is proprietary software; using the deletion and AI features requires an active membership (see README). The source is not licensed for redistribution." Add `LICENSE` to the archive `files` list in `.goreleaser.yaml`.
- [ ] **Step 3: `CHANGELOG.md` `## [0.5.0]`** — Added: membership gate, `license` command, 7-day trial, doctor and home status; Changed: GoReleaser 2.19.0 / `install_steps`; Removed: root-level licence check.
- [ ] **Step 4: README** Install block unchanged for now (private); new "Membership" section.
- [ ] **Step 5: Gate check and tag** (on `main` after merging `m6/membership`):

Run: `make release-check NEXT=v0.5.0 && git tag -a v0.5.0 -m "v0.5.0: membership gate" && git push origin main v0.5.0`
Expected: release workflow green; `gh release view v0.5.0 --repo shiva2021/rootout` shows notes; the tap cask updated with `postflight_steps`.

- [ ] **Step 6: Install and smoke**: `HOMEBREW_GITHUB_API_TOKEN=$(gh auth token) brew upgrade --cask rootout && rootout --version && rootout doctor | grep Membership`.
- [ ] **Step 7: Commit docs** (before the tag, in the same PR/merge): `git commit -m "docs: D-033/D-034, M6 membership gate shipped in v0.5.0"`

---

# Part C · Public releases repo (D-033) → v0.5.1

Manual steps by Shivang unless marked (agent). Do them in order; each is reversible until step 6.

- [ ] **C1.** Create `shiva2021/rootout-releases` (public) with a README: what rootout is, `brew tap shiva2021/rootout && brew trust shiva2021/rootout && brew install --cask rootout`, direct-download + `xattr -d com.apple.quarantine rootout`, "membership after a 7-day trial", link to the keys page. No links into the private repo's docs.
- [ ] **C2.** Make `shiva2021/homebrew-rootout` public (Settings → Danger zone).
- [ ] **C3.** Fine-grained PAT `RELEASE_GITHUB_TOKEN`: resource owner shiva2021, repositories `rootout-releases` + `homebrew-rootout`, Contents read/write, 1-year expiry. `gh secret set RELEASE_GITHUB_TOKEN --repo shiva2021/rootout`; `gh secret delete TAP_GITHUB_TOKEN --repo shiva2021/rootout`.
- [ ] **C4 (agent).** `.goreleaser.yaml`: `release.github.name: rootout-releases`, `release.token: "{{ .Env.RELEASE_GITHUB_TOKEN }}"`; in `homebrew_casks`: delete `url` and `custom_block`, `repository.token` → `RELEASE_GITHUB_TOKEN`, `homepage: https://github.com/shiva2021/rootout-releases`, caveats → "rootout is not code-signed; the quarantine attribute is cleared on install. Deletion and AI features need a membership after a 7-day trial: run `rootout license`." `.github/workflows/release.yml`: env `RELEASE_GITHUB_TOKEN: ${{ secrets.RELEASE_GITHUB_TOKEN }}` replaces `TAP_GITHUB_TOKEN`. Verify: `make snapshot && grep -c 'DownloadStrategy' dist/homebrew/Casks/rootout.rb` → `0`; `url` is a plain `https://github.com/shiva2021/rootout-releases/releases/download/...` line; `ruby -c` OK.
- [ ] **C5 (agent).** Docs: README install block → the three-line brew sequence (no token); `docs/kb/release.md` rewrite "Install from the tap", "Tokens" (`RELEASE_GITHUB_TOKEN` only), "Known limits" (drop the token limit); D-033 status → Accepted; `CHANGELOG.md` `## [0.5.1]` ("Changed: releases published to the public rootout-releases repo; no token needed to install"). Commit: `release: publish to the public rootout-releases repo (D-033)`.
- [ ] **C6.** `make release-check NEXT=v0.5.1`, tag, push. Verify: `gh release view v0.5.1 --repo shiva2021/rootout-releases`; the tap commit has the plain cask.
- [ ] **C7.** In a shell with **no** `HOMEBREW_GITHUB_API_TOKEN`: `brew untap shiva2021/rootout; brew tap shiva2021/rootout && brew trust shiva2021/rootout && brew install --cask rootout && rootout --version` → `v0.5.1`, no quarantine prompt, `doctor` runs.
- [ ] **C8 (agent).** `docs/HANDOVER.md`: status, what shipped, next steps (signing, Intel RAM tiers, open manual checks), gotchas learned.

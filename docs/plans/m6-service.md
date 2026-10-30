# M6 · Key service Implementation Plan (Part B)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Cloudflare Worker that turns an active Patreon membership into a machine-bound Ed25519 licence key, renews keys, issues one 7-day trial per Mac, and keeps the creator token alive.

**Architecture:** One Worker (`service/`) with four HTTP routes and one cron. Patreon is called server-side with the client secret and the creator token; all state is in one KV namespace. Keys are produced by a `sign.ts` module that mirrors `internal/license` byte-for-byte, proven by a golden key generated from the Go side.

**Tech Stack:** TypeScript, Cloudflare Workers runtime (WebCrypto `Ed25519`, KV, cron triggers), `wrangler` 4.x, `vitest` + `@cloudflare/vitest-pool-workers`. No framework; a hand-rolled router. No npm runtime dependencies.

**Spec:** `docs/plans/m6-membership.md` (Design → Key format, State machine, Service).

## Global Constraints

- Key format identical to Go: `ro1.<b64url(payload)>.<b64url(sig)>`, unpadded; payload is JSON with keys in the order `v, kind, sub, mid, iat, exp` and no spaces (`JSON.stringify` of an object built in that order, `exp` omitted for admin).
- Patron keys `exp = iat + 35*86400`; trial `iat + 7*86400`. Device cap **2** per Patreon user, oldest replaced.
- Active patron: `patron_status === "active_patron" && (currently_entitled_amount_cents > 0 || is_gifted === true)`, read from the member record fetched with the creator token; the user's own `identity` data is the fallback only when the creator token is rejected (401).
- Secrets (wrangler secrets): `PATREON_CLIENT_ID`, `PATREON_CLIENT_SECRET`, `PATREON_CAMPAIGN_ID`, `SIGNING_KEY` (PKCS#8 base64), `STATE_SECRET`, `ALERT_GITHUB_TOKEN`. Vars (`wrangler.toml`): `PATREON_URL` (membership page), `ALERT_REPO = shiva2021/rootout`, `PUBLIC_ORIGIN`.
- KV namespace binding `KEYS`. Keys: `creator` → `{access, refresh, updated}`; `creator_prev` → same; `trial:<mid>` → `{iat}`; `devices:<userId>` → `[{mid, iat}]` (newest last); `member:<userId>` → `"<memberId>"`.
- Patreon endpoints: authorize `https://www.patreon.com/oauth2/authorize`, token `https://www.patreon.com/api/oauth2/token`, API base `https://www.patreon.com/api/oauth2/v2`. Rate limit 100 req / 2 s per client: never loop over members.
- JSON responses only for `/renew` and `/trial`: `200 {"key"}`, `403 {"reason"}`, `409 {"reason":"trial_used"}`, `503 {"reason":"creator_token"}`, `400 {"reason"}`.
- Stored personal data: Patreon user id, member id, hashed machine ids. Never names or emails.
- Each task: tests first (`npm test`), `npm run typecheck` clean, one commit.

## Review Focus

1. Patreon returns `patron_status: null` and a member record with only `id`/`type` (fields not requested or a free member) → treated as **not active**, never as active (S3 test `callback: null patron_status is not active`).
2. The `state` parameter is replayed or forged (another user's `mid`, expired nonce) → 400, no key minted (S3 test `callback: tampered state is rejected`).
3. `/renew` with a trial or admin key, or a key signed by another keypair → 403 / 400, never a new patron key (S4 tests).
4. KV eviction of the oldest device while that Mac is mid-renewal → `device_replaced` 403 on its next renew, not a 500 (S4 test `renew: evicted device gets device_replaced`).
5. The creator token refresh succeeds at Patreon but the KV write fails → the old pair stays readable (`creator_prev`) and the next run retries; no silent loss (S5 test `cron: KV put failure keeps the previous pair`).

---

### Task S1: Scaffold, signing module and the Go golden key

**Files:**
- Create: `service/package.json`, `service/tsconfig.json`, `service/wrangler.toml`, `service/vitest.config.ts`, `service/.gitignore` (`node_modules/`, `.wrangler/`, `.dev.vars`)
- Create: `service/src/sign.ts`, `service/test/sign.test.ts`
- Create: `service/test/fixtures/golden.json` (copied from `internal/license/testdata/golden.json`, Part A Task 2)
- Modify: `.github/workflows/ci.yml` (job `service`: Node 24 LTS, `npm ci`, `npm test`, `npm run typecheck`, `working-directory: service`, `paths` filter not used so it always runs)

**Interfaces:**
- Produces (`sign.ts`):
  ```ts
  export type Kind = "patron" | "trial" | "admin";
  export interface Claims { v: 1; kind: Kind; sub: string; mid: string; iat: number; exp?: number }
  export const PATRON_LIFE = 35 * 86400; export const TRIAL_LIFE = 7 * 86400;
  export function encodeClaims(c: Claims): Uint8Array            // canonical JSON bytes, key order v,kind,sub,mid,iat[,exp]
  export async function importPrivateKey(pkcs8b64: string): Promise<CryptoKey>
  export async function importPublicKey(rawHex: string): Promise<CryptoKey>
  export async function sign(c: Claims, priv: CryptoKey): Promise<string>  // "ro1.x.y"
  export async function verify(key: string, pub: CryptoKey): Promise<Claims | null> // null on any failure; validates like Go's Claims.Valid()
  export function isMid(s: string): boolean                        // /^[0-9a-f]{32}$/
  ```
- Test keypair: seed = SHA-256 of the UTF-8 string `rootout-test-seed`; private key = PKCS#8 wrapping of that 32-byte seed (Ed25519 PKCS#8 prefix `302e020100300506032b657004220420` + seed); public key from `golden.json` `pub_hex`.

- [ ] **Step 1: Scaffold**

Run: `cd service && npm init -y && npm i -D typescript wrangler vitest @cloudflare/vitest-pool-workers @cloudflare/workers-types` and pin exact versions in `package.json` (`"wrangler": "4.x.y"` etc. as installed). Scripts: `dev: wrangler dev`, `deploy: wrangler deploy`, `test: vitest run`, `typecheck: tsc --noEmit`. `wrangler.toml`: `name = "rootout-keys"`, `main = "src/index.ts"`, `compatibility_date = "2026-10-01"`, `[[kv_namespaces]] binding = "KEYS"`, `[triggers] crons = ["17 3 * * *"]`, `[vars]` for `PATREON_URL`, `ALERT_REPO`, `PUBLIC_ORIGIN`.

- [ ] **Step 2: Write failing tests** `test/sign.test.ts`:
  - `encodes claims in Go key order` (bytes equal `{"v":1,"kind":"patron","sub":"12345678","mid":"0123456789abcdef0123456789abcdef","iat":1730246400,"exp":1733270400}`)
  - `signs the golden claims to the golden key` (sign with the seed-derived private key equals `golden.json.key` exactly; Ed25519 is deterministic)
  - `verifies the golden key with the golden public key`
  - `rejects: wrong prefix, tampered payload, foreign key, patron without exp, bad mid` (each → `null`)
  - `admin claims omit exp`

- [ ] **Step 3: Run** `npm test` → FAIL (module missing). **Step 4: Implement `sign.ts`** with `crypto.subtle.importKey("pkcs8" | "raw", …, {name: "Ed25519"}, …)`, `sign`/`verify` with `"Ed25519"`. **Step 5: Run** → PASS.

- [ ] **Step 6: CI job** in `.github/workflows/ci.yml`:

```yaml
  service:
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: service } }
    steps:
      - uses: actions/checkout@v7.0.1
      - uses: actions/setup-node@v6.1.0
        with: { node-version: 24, cache: npm, cache-dependency-path: service/package-lock.json }
      - run: npm ci
      - run: npm run typecheck
      - run: npm test
```

(Check the current `actions/setup-node` tag before pinning.)

- [ ] **Step 7: Commit** `git add service .github/workflows/ci.yml && git commit -m "service: Worker scaffold and Ed25519 signing module matching internal/license"`

---

### Task S2: Router, KV helpers, Patreon client, state tokens

**Files:**
- Create: `service/src/index.ts` (fetch + scheduled entry), `service/src/router.ts`, `service/src/kv.ts`, `service/src/patreon.ts`, `service/src/state.ts`, `service/src/env.ts`
- Create: `service/test/patreon.test.ts`, `service/test/state.test.ts`, `service/test/kv.test.ts`

**Interfaces:**
- `env.ts`: `export interface Env { KEYS: KVNamespace; PATREON_CLIENT_ID: string; PATREON_CLIENT_SECRET: string; PATREON_CAMPAIGN_ID: string; SIGNING_KEY: string; STATE_SECRET: string; ALERT_GITHUB_TOKEN: string; PATREON_URL: string; ALERT_REPO: string; PUBLIC_ORIGIN: string }`
- `kv.ts`:
  ```ts
  export interface TokenPair { access: string; refresh: string; updated: number }
  export async function getCreator(kv): Promise<TokenPair | null>   // "creator", falls back to "creator_prev"
  export async function putCreator(kv, pair): Promise<void>          // copies current → creator_prev, then writes creator
  export interface Device { mid: string; iat: number }
  export async function getDevices(kv, userId): Promise<Device[]>
  export async function addDevice(kv, userId, mid, now): Promise<{ devices: Device[]; evicted: Device | null }> // cap 2; if mid present → no write, no eviction; else drop oldest by iat when full
  export async function hasDevice(kv, userId, mid): Promise<boolean>
  export async function trialSeen(kv, mid): Promise<boolean>; export async function markTrial(kv, mid, now)
  export async function getMember(kv, userId): Promise<string | null>; export async function putMember(kv, userId, memberId)
  ```
- `patreon.ts`:
  ```ts
  export function authorizeUrl(env, state: string): string  // response_type=code, client_id, redirect_uri=PUBLIC_ORIGIN+"/callback", scope=identity, state
  export async function exchangeCode(env, code: string): Promise<TokenPair>
  export async function refreshToken(env, refresh: string): Promise<TokenPair>
  export interface Membership { userId: string; memberId: string | null; patronStatus: string | null; cents: number; gifted: boolean }
  export async function identityMembership(accessToken: string, campaignId: string): Promise<Membership>  // GET /identity?include=memberships.campaign&fields[member]=patron_status,currently_entitled_amount_cents,is_gifted&fields[campaign]=; picks the membership whose campaign id === campaignId; memberId null when none
  export async function memberById(creatorAccess: string, memberId: string): Promise<Membership> // GET /members/{id}?fields[member]=…&include=user
  export class PatreonAuthError extends Error {}   // 401 from Patreon (dead token)
  export function isActive(m: Membership): boolean // patronStatus === "active_patron" && (cents > 0 || gifted)
  ```
- `state.ts`: `export async function signState(env, payload: {mid: string; nonce: string; exp: number}): Promise<string>` (HMAC-SHA-256 with `STATE_SECRET`, base64url `payload.sig`), `export async function verifyState(env, s: string, now: number): Promise<{mid: string} | null>` (exp 10 min).
- `router.ts`: `export type Handler = (req: Request, env: Env, ctx: ExecutionContext) => Promise<Response>`; `export function route(req, table: Record<"GET /"|"GET /callback"|"POST /renew"|"POST /trial", Handler>)`; unknown → 404; `export const json = (status: number, body: unknown) => Response`.

- [ ] **Step 1: Write failing tests** using `fetchMock` from `cloudflare:test` for Patreon and the Miniflare KV binding:
  - `patreon.test.ts`: `exchangeCode posts form fields and parses tokens`; `identityMembership picks our campaign and ignores others`; `identityMembership returns memberId null when not a member`; `memberById maps fields`; `isActive table` (active+cents, active+gifted, active+0 cents not gifted → false, declined → false, null → false); `401 throws PatreonAuthError`.
  - `state.test.ts`: `round trip`; `expired → null`; `tampered sig → null`.
  - `kv.test.ts`: `addDevice caps at 2 and evicts oldest`; `addDevice with an existing mid writes nothing and evicts nothing`; `putCreator keeps the previous pair`; `getCreator falls back to creator_prev`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `git commit -m "service: router, KV helpers, Patreon client and signed state"`

---

### Task S3: Key page and OAuth callback

**Files:**
- Create: `service/src/page.ts` (HTML templates as template strings: `homePage(mid, authorizeUrl)`, `keyPage(key, devices, mid)`, `deniedPage(reason, patreonUrl)`, `errorPage(msg)`), `service/src/routes/home.ts`, `service/src/routes/callback.ts`
- Create: `service/test/callback.test.ts`, `service/test/home.test.ts`

**Interfaces:**
- Consumes: S1 `sign`, S2 everything.
- `GET /?m=<mid>` → 200 HTML; a form (`method=GET action=/start`) with field `m` prefilled when `isMid` and a "Log in with Patreon" submit button; without a valid `m` the page explains how to get the machine code (`rootout license`). The field is editable, so the form must act on edits:
- `GET /start?m=<mid>` → `isMid` else 400 page → 302 to `authorizeUrl(env, await signState(env, {mid, nonce: crypto.randomUUID(), exp: now+600}))`. Add `"GET /start"` to the router table.
- `GET /callback?code=&state=` → verify state (400 on failure) → `exchangeCode` → `identityMembership(user token)` → if `memberId`: try `memberById(creator)`; on `PatreonAuthError` fall back to the identity membership and `ctx.waitUntil(alert(env, "creator token rejected"))` → `isActive`? → `addDevice`, `putMember`, `sign({v:1, kind:"patron", sub:userId, mid, iat:now, exp:now+PATRON_LIFE})` → `keyPage` (200, `Cache-Control: no-store`). Not active → `deniedPage(reason)` 200 with reasons `free_member | declined_patron | former_patron | not_a_member`.

- [ ] **Step 1: Write failing tests** (`SELF.fetch` from `cloudflare:test`, Patreon via `fetchMock`, env secrets via `vitest.config.ts` `miniflare.bindings` with the test PKCS#8 and a fixed `STATE_SECRET`):
  - `home: prefills a valid mid in a form posting to /start`
  - `home: without m explains rootout license`
  - `start: valid mid redirects 302 to patreon.com/oauth2/authorize with scope=identity and a verifiable state carrying that mid`
  - `start: bad mid → 400`
  - `callback: active patron gets a patron key bound to the state mid` (decode with `verify` + golden pub; `exp-iat === 35*86400`; KV has `devices:<uid>` and `member:<uid>`)
  - `callback: third device evicts the oldest` (pre-seed two devices)
  - `callback: null patron_status is not active` → denied page contains `free`
  - `callback: declined patron denied` → `declined`
  - `callback: tampered state is rejected` → 400, no Patreon token call
  - `callback: creator token 401 falls back to identity data and still issues when active`
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `git commit -m "service: key page and Patreon OAuth callback with device cap"`

---

### Task S4: `/renew` and `/trial`

**Files:**
- Create: `service/src/routes/renew.ts`, `service/src/routes/trial.ts`, `service/test/renew.test.ts`, `service/test/trial.test.ts`

**Interfaces:**
- `POST /renew` body `{key}` → `verify(key, pub)` (public key derived from `SIGNING_KEY` at startup; cache the `CryptoKey`s in module scope) → null → 400 `{"reason":"bad_key"}`; `kind !== "patron"` → 403 `{"reason":"not_renewable"}`; `!hasDevice(sub, mid)` → 403 `device_replaced`; `getMember(sub)` null → 403 `unknown_member`; `memberById(creator)` → `PatreonAuthError` → 503 `creator_token` (+ alert); `!isActive` → 403 `patronStatus ?? "inactive"`; else 200 `{key: sign({... same sub, mid, iat: now, exp: now+PATRON_LIFE})}` with no KV write.
- `POST /trial` body `{mid}` → `!isMid` → 400; `trialSeen` → 409 `{"reason":"trial_used"}`; else `markTrial`, 200 `{key: sign({v:1, kind:"trial", sub:"trial", mid, iat, exp: iat+TRIAL_LIFE})}`.
- Both: `Content-Type` must be JSON, body ≤ 4 KiB, otherwise 400.
- **KV write budget.** The Workers Free plan allows roughly 1,000 KV writes per day (verify the current figure at docs.cloudflare.com/kv/platform/limits when implementing). `/trial` is an unauthenticated write per call, so a trivial loop could exhaust the day's quota and block real trials and renewals. Therefore: (a) a successful `/renew` performs **no** KV write (do not refresh the device `iat`; eviction order uses issue time only); (b) `/trial` and `/renew` go through a Workers **rate-limit binding** (`[[unsafe.bindings]] type = "ratelimit"` or its current stable name; confirm it works without a zone) keyed by client IP: 5 requests per minute → 429 `{"reason":"rate_limited"}`; (c) the CLI treats 429 like a network error. The runbook lists the quota as a known limit and the `wrangler kv` command to inspect write counts.

- [ ] **Step 1: Write failing tests**: `renew: active member gets a fresh key with same sub and mid and performs no KV write` (spy on `KEYS.put`); `renew: sixth call in a minute from one IP → 429`; `trial: sixth call in a minute → 429`; `renew: trial key → 403 not_renewable`; `renew: admin key → 403`; `renew: foreign signature → 400`; `renew: evicted device gets device_replaced`; `renew: declined → 403 declined_patron`; `renew: creator 401 → 503 creator_token`; `trial: first call issues 7-day key and marks KV`; `trial: second call 409`; `trial: bad mid 400`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `git commit -m "service: renew and trial endpoints"`

---

### Task S5: Cron refresh of the creator token and GitHub alert

**Files:**
- Create: `service/src/cron.ts`, `service/src/alert.ts`, `service/test/cron.test.ts`, `service/test/alert.test.ts`

**Interfaces:**
- `alert.ts`: `export async function alert(env, title: string, body: string): Promise<void>` → search open issues in `ALERT_REPO` with label `rootout-keys` and the same title (`GET /repos/{repo}/issues?labels=rootout-keys&state=open`); comment if found, else create with the label. Token `ALERT_GITHUB_TOKEN`, `User-Agent: rootout-keys`. Never throws (logs on failure).
- `cron.ts`: `export async function refreshCreator(env, now: number): Promise<"refreshed" | "skipped" | "failed">` → `getCreator` null → `alert("creator token missing")`, `failed`; if `now - updated < 20 days` → `skipped`; else `refreshToken(refresh)` → `putCreator` → `refreshed`; on error → `alert("creator token refresh failed", err)`, `failed`. `index.ts` `scheduled` calls it with `ctx.waitUntil`.

- [ ] **Step 1: Write failing tests**: `cron: refreshes after 20 days and stores the new pair with prev kept`; `cron: skips a fresh pair`; `cron: Patreon error alerts and keeps the pair`; `cron: KV put failure keeps the previous pair` (mock `KEYS.put` to throw once); `alert: comments on an existing open issue`; `alert: creates when none`; `alert: GitHub 500 does not throw`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS; `npm run typecheck` clean.
- [ ] **Step 5: Commit** `git commit -m "service: daily creator-token refresh with GitHub-issue alerts"`

---

### Task S6: Deploy (Shivang, with the agent's commands)

- [ ] **S6.1** Cloudflare account; `cd service && npx wrangler login`; `npx wrangler kv namespace create KEYS` → paste the id into `wrangler.toml`.
- [ ] **S6.2** Patreon: create the campaign (any paid tier); Clients & API Keys → new v2 client, redirect URI `https://rootout-keys.<account>.workers.dev/callback`; note client id/secret, campaign id (`GET /api/oauth2/v2/campaigns` with the creator token), and copy the **Creator's Access Token** and refresh token.
- [ ] **S6.3** Secrets: `wrangler secret put` for each of `PATREON_CLIENT_ID`, `PATREON_CLIENT_SECRET`, `PATREON_CAMPAIGN_ID`, `SIGNING_KEY` (the PKCS8 base64 printed by `rootout-keys gen`), `STATE_SECRET` (`openssl rand -base64 32`), `ALERT_GITHUB_TOKEN` (fine-grained, `shiva2021/rootout`, Issues read/write). Seed the creator pair: `wrangler kv key put --remote --binding KEYS creator '{"access":"…","refresh":"…","updated":<epoch>}'`. Set `PATREON_URL` and `PUBLIC_ORIGIN` vars.
- [ ] **S6.4** `npm run deploy`; open `https://rootout-keys.<account>.workers.dev/?m=<this Mac's code>` → page renders; `curl -s -X POST …/trial -H 'content-type: application/json' -d '{"mid":"<code>"}'` → key; second call → 409. `wrangler kv key delete --remote --binding KEYS trial:<code>` afterwards.
- [ ] **S6.5** Paste the host into `license.ServiceURL` (Part A Task 7) and the runbook.
- [ ] **S6.6** First real pledge from a second Patreon account (a friend, or a second account of Shivang's; a creator cannot pledge to their own campaign). On this Mac the admin key occupies the default key file, so run the patron flow with `ROOTOUT_LICENSE_FILE=/tmp/patron.key`: open the keys page with this Mac's code, log in as the patron, `ROOTOUT_LICENSE_FILE=/tmp/patron.key rootout license <key>` → `doctor` shows `member`. Renewal check without waiting 28 days: mint an already-expired patron key for that user with `rootout-keys sign --kind patron --sub <userId> --mid <code> --days 6` (renewal is due under 7 days; `sign` rejects negative days), install it the same way, run `remove Spotify --dry-run` (free, no renew) then `remove Spotify` → the renew succeeds and the key file carries a fresh `exp`. Then cancel the pledge on Patreon and repeat with another expired key → 403 `former_patron` and the denial shows that reason. Record every outcome in `docs/HANDOVER.md`.

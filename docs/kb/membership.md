# Membership: keys, admin keys and the key service

Read when touching the gate, issuing or rotating keys, or operating the key service. Decision: D-034.

## How keys work

- **Format:** `ro1.<b64url payload>.<b64url signature>`. The payload is JSON claims (`v`, `kind`, `sub`, `mid`, `iat`, `exp`). The signature is Ed25519 over the payload bytes.
- **Kinds:**

  | Kind | Lifetime | Issued to |
  |---|---|---|
  | `patron` | 35 days | A paying Patreon member |
  | `trial` | 7 days | A first-time trial on one Mac |
  | `admin` | none (no `exp`) | Shivang's own Macs |

- **Machine bound:** every kind carries `mid`, the first 32 hex characters of SHA-256 of `"rootout:" + IOPlatformUUID`. `rootout license` prints it. A key pasted on another Mac fails.
- **Renewal:** silent when under 7 days remain or the key has expired, at most once per 24 hours. A 14-day grace applies only when the service could not be reached. A 403 denial is remembered beside the key until a renewal succeeds or a new key is pasted.
- **Verification:** the public key is `license.PublicKeyHex` in `internal/license/claims.go`. Empty means every key fails closed.

## Admin key per Mac

Admin keys never expire but stay machine bound, so each Mac needs its own.

1. On the target Mac run `rootout license`. It prints the machine code.
2. On the Mac that holds the private key run:

   ```bash
   go run ./cmd/rootout-keys admin --mid <machine code>
   ```

3. On the target Mac run `rootout license <key>`.

`rootout-keys inspect <key>` shows the claims. `rootout-keys sign --kind patron|trial --sub <id> --mid <code> --days N` issues test keys; a negative `--days` is rejected.

## Key backup and rotation

- The private key lives at `~/.config/rootout-keys/private.pem` (mode 0600). `rootout-keys gen` refuses to overwrite it.
- **Backup:** the password-manager entry "rootout signing key" holds both the PEM contents and the PKCS8 base64 (the Worker's `SIGNING_KEY`). Confirm the entry opens before relying on it.
- **Rotation:** generate a new keypair, add its public key as a second accepted constant in `internal/license`, and ship one release cycle that accepts both. Move the Worker's `SIGNING_KEY` to the new key. In the following release remove the old constant. Keys signed by the old key stop working then; their holders renew from the keys page.
- If the private key is lost with no backup, no existing key can be renewed. Ship a new public key and have every member fetch a new key.

## Service operations

The key service is the Cloudflare Worker `rootout-keys` at `rootout-keys.<account>.workers.dev`.

- **Secrets:** `PATREON_CLIENT_ID`, `PATREON_CLIENT_SECRET`, `PATREON_CAMPAIGN_ID`, `SIGNING_KEY` (the PKCS8 base64), `STATE_SECRET`, `ALERT_GITHUB_TOKEN`.
- **KV binding `KEYS`:**

  | Key | Holds |
  |---|---|
  | `creator`, `creator_prev` | The creator's Patreon token pair, current and previous |
  | `trial:<mid>` | A trial already used on that Mac |
  | `devices:<userId>` | The member's Macs, at most 2, oldest replaced |
  | `member:<userId>` | The member's cached Patreon status |

- **Re-seed the creator token** when the alert says it failed to refresh:

  ```bash
  wrangler kv key put --binding KEYS creator '{"access":"…","refresh":"…","updated":<epoch seconds>}'
  ```

- **Reset a trial** (a member lost their Mac, or you are testing):

  ```bash
  wrangler kv key delete --binding KEYS trial:<mid>
  ```

- **Read the device list:** `wrangler kv key get --binding KEYS devices:<userId>`.
- **Alerts** arrive as a GitHub issue labelled `rootout-keys` on `shiva2021/rootout`. Fix the cause, then close the issue.
- **Known limit:** the Workers Free plan allows about 1,000 KV writes a day. Heavy renewal traffic can hit it; the fix is the paid plan.

## Support answers

- **Declined payment.** Patreon shows the member as `declined_patron`, so renewal is denied. Ask them to fix the card on Patreon, then run any gated command or paste a new key from the keys page.
- **Device replaced.** A third Mac took this one's slot (two allowed, oldest replaced). Fetch a key again from the keys page on this Mac, which takes a slot back from the oldest.
- **Lost key.** Fetch again from the keys page. It re-issues for the same Mac without using a device slot.

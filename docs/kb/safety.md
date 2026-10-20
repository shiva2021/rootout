# Safety rules

Everything that deletes goes through `internal/safety`. Read this before touching `safety`, `leftover`, `plan`, `priv`, `trash` or `ext.Uninstall`.

## Invariants
1. Refuse to start as root (`os.Geteuid() == 0`).
2. Every planned path is absolute, `filepath.Clean`ed, and under an allowed prefix: `/Applications`, `~/Applications`, `~/Library`, `/Library`, opt-in `~/.config`. Never under `/System`, `/usr`, `/bin`, `/sbin`, `/private/var/db`, `/Library/Apple`. Never an allowed prefix itself or a direct child of `~/Library` or `/Library` (we delete `~/Library/Caches/com.foo`, never `~/Library/Caches`). `Lstat`, never follow symlinks. Depth ≥ 3 under `$HOME`.
3. Protected apps (under `/System`, bundle id `com.apple.*`, `restricted` flag) are shown but never removable.
4. `safety.Validate` runs when the plan is built and again inside `Execute` immediately before each deletion.
5. The privileged step receives a fixed argv list, re-validated with the same function, in a single `osascript … with administrator privileges` call. No shell interpolation of user data. Quoting is tested with names containing `'"$;` and spaces.
6. No deletion without explicit confirmation unless `--yes`. `--yes --permanent` prints a red banner.
7. Name-based matches need `safety.NameOK`: name length ≥ 4, not in the denylist (every top-level `~/Library` dir name, Apple app names, `R`, `Go`, `Java`, vendor names), and corroborated by the bundle id. Exact directory-name equality, case-insensitive, never substring or glob. Name matches are never the sole basis for anything under `/Library` or `Group Containers`.
8. `heur` never executes extension code, never follows symlinks out of the extension dir, caps files at 2 MB and extensions at 50 MB / 2000 files.
9. Tests inject `fsx.Env`; `testutil.RequireSandbox` fails any test pointed at the real home.
10. LLM prompts contain findings and snippets only; `--json` output never includes keys.

## Leftover rules (B = bundle id, B* = `B.` prefix, T.B = TeamID-prefixed, N = guarded name)
| Location | Key |
|---|---|
| `~/Library/Preferences/<B>.plist`, `<B>.*.plist`, `ByHost/<B>.*` | B, B* |
| `~/Library/Caches/<key>`, `Logs/<key>`, `Application Support/<key>` | B, B*, N |
| `~/Library/Containers/<key>` | B, T.B |
| `~/Library/Group Containers/<key>` | T.B, `group.<B>`; never N |
| `~/Library/Saved Application State/<B>.savedState`, `HTTPStorages/<B>`, `Cookies/<B>.binarycookies`, `WebKit/<B>` | B, B* (helper ids such as `<B>.helper`) |
| `~/Library/Application Scripts/<B>` | B |
| `~/Library/LaunchAgents`, `/Library/LaunchAgents`, `/Library/LaunchDaemons` | B* or plist program path inside the .app |
| `/Library/PrivilegedHelperTools/<B>*` | B* (root) |
| `/Library/Application Support/<N>`, `/Library/Preferences/<B>.plist`, `/Library/Caches/<B>` | B, N (root) |
| `~/Library/Logs/DiagnosticReports/<N>-*.ips`, `/Library/Logs/DiagnosticReports/<N>_*.diag` | N exact prefix + `-`/`_` and a crash-report extension |
| pkg receipts (`pkgutil --pkgs` filtered by B*) | list files still present; `--forget` on execute |
| Login items (`sfltool dumpbtm`, 3 s timeout: it blocks without Full Disk Access) | report only |
| `~/.config/<N>`, `~/.<N>` | N, off by default (`--dotfiles`) |

## Known non-matches (by design)
- Sibling bundle ids from the same vendor (`com.spotify.installer` next to `com.spotify.client`) are not matched: a vendor-wide rule would sweep unrelated apps (`com.google.*`). Use `--also` or the TUI "add path".
- Third-party SDK folders nested under another vendor's directory (`Application Support/io.branch/<id>`) are not matched.
- Root-owned paths are deleted permanently even in Trash mode (the Trash is per-user); the confirm screen says so.

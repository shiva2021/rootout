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

## Files rootout writes

Only the config file (`internal/config.Save`) and the saved-assessments file (`internal/assesscache`, `~/.cache/rootout/assessments.json`), both atomically via temp file + rename, 0600 in a 0700 directory. Neither is an app leftover: nothing under `~/.cache/rootout` or `~/.config/rootout` matches a leftover rule. `config cache clear` is the one deletion outside `Validate` (D-038): it removes the path only when it is a regular file that parses as a rootout assessments file, and refuses anything else `ROOTOUT_CACHE` may point at.

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

## Orphaned leftovers (`rootout orphans`, D-020)
Detection: `leftover.FindOrphans`. Only `~/Library` rows of the rules table are read, and only entries whose name parses as a reverse-DNS id (`leftover.ParseOwner`: strips `.plist`/`.savedState`/`.binarycookies`, a team-id or `group.`/`groups.` prefix and a ByHost UUID; needs ≥ 3 segments; rejects numeric segments and file-extension segments such as `log`, `old`, `sqlite`). Plain names are never candidates.

An id is owned (not an orphan) when any of these hold:
- an installed app has the id, an extension of it (`<id>.helper`) or a prefix of it (vendor folders);
- it contains `com.apple.` or is an Apple alias (`is.workflow.*`, Shortcuts);
- it is a shared analytics/attribution SDK id (`io.branch.`, `com.crashlytics.`, `com.segment.`, …) whose owner cannot be inferred;
- any app bundle on disk carries it (`mdfind` for application bundles, Info.plist read);
- a loaded launchd job (`launchctl list`) or a running process (`ps -axo comm=`) uses it.

Groups changed within 30 days (`--min-age`) are marked recently used. Groups sharing a distinctive id segment with an installed app carry `VendorInstalled` ("vendor still installed: Perplexity") and stay listed for the user to judge. Nothing is pre-selected; removal needs the typed word. `/Library` rows and receipts are report-only in this version.

## Full Disk Access (D-026)
Entries directly under `~/Library/Containers` are TCC-protected: the user, Finder, `/usr/bin/trash` and root are all refused unless the *terminal app* has Full Disk Access (verified 2026-10-21: granting it to Warp made the removal work). `tcc.FullDiskAccess(env)` probes by listing `~/Library/Safari`, then `~/Library/Mail`, then `~/Library/Application Support/com.apple.TCC`; `fs.ErrPermission` = denied, success = granted, no folder = unknown (no warning). `doctor` shows a "Full Disk Access" line naming the terminal from `TERM_PROGRAM`. `plan.MarkAccess` (called by `plan.Build` and by `rootout orphans`) notes affected items "needs Full Disk Access" and sets `Plan.Warning`; both confirm views show the tag and the line. The run is not blocked: such paths fail one by one with the hint added in `trash.withHint`. `Group Containers` is not probed; nobody has confirmed it is protected the same way.

# VS Code extension security audit — 2026-10-20

Scope: `~/.vscode/extensions` on this Mac (VS Code 1.123.2, arm64). No Insiders, Cursor or Windsurf installs found.
Method: manifest review (publisher, activation, install hooks, capabilities), static scan of bundled JS for
child_process / eval / base64 / shell / credential-path usage, external host extraction, dependency version check,
and review of VS Code user settings, mcp.json, argv.json and extension state.

## Inventory

| Extension | Version | Publisher | Source | State | Risk |
|---|---|---|---|---|---|
| heisebaiyun.kiro-for-cc | 0.2.9 | heisebaiyun (unverified individual) | gallery | **uninstalled, folder left on disk** | HIGH |
| leetcode.vscode-leetcode | 0.18.4 | LeetCode | gallery | **uninstalled, folder left on disk** | HIGH (stale, creds on disk) |
| mongodb.mongodb-vscode | 1.18.0 | MongoDB (verified) | gallery | active | MEDIUM |
| ms-python.python | 2026.10.0 | Microsoft | gallery | active | LOW |
| ms-python.vscode-pylance | 2026.4.1 | Microsoft | gallery | active | LOW |
| ms-python.debugpy | 2026.6.0 | Microsoft | gallery | active | LOW |
| ms-python.flake8 | 2026.6.0 | Microsoft | gallery | active | LOW |
| ms-python.vscode-python-envs | 1.40.0 | Microsoft | gallery | active | LOW |
| ms-toolsai.jupyter | 2025.9.1 | Microsoft | gallery | active | LOW |
| ms-toolsai.jupyter-keymap | 1.1.2 | Microsoft | gallery | active | LOW |
| ms-toolsai.jupyter-renderers | 1.3.0 | Microsoft | gallery | active | LOW |
| ms-toolsai.vscode-jupyter-cell-tags | 0.1.9 | Microsoft | gallery | active | LOW |
| ms-toolsai.vscode-jupyter-slideshow | 0.1.6 | Microsoft | gallery | active | LOW |
| mechatroner.rainbow-csv | 3.24.1 | mechatroner | gallery | active | LOW |

## Findings

### 1. heisebaiyun.kiro-for-cc 0.2.9 — HIGH
- Third-party wrapper around Claude Code from an individual publisher; activates on every VS Code startup (`onStartupFinished`).
- Opens a terminal and runs `claude --permission-mode bypassPermissions` (dist/providers/claudeCodeProvider.js:206). That
  disables all Claude Code permission prompts for the session it starts.
- Shells out to `claude mcp list` / `claude mcp get <name>` (mcpExplorerProvider.js:124,177).
- Writes under `~/.claude/` (agents/kfc, system-prompts, specs, steering, settings.json, CLAUDE.md).
- Phones home to api.github.com for update checks.
- Status: already marked obsolete in `.obsolete`, so VS Code no longer loads it, but the 1 MB folder is still on disk and
  a `heisebaiyun.kiro-for-cc` key remains in `state.vscdb`. No leftovers found in `~/.claude/` (no agents/kfc, no
  system-prompts, no kfc/kiro/bypassPermissions strings in settings or CLAUDE.md).

### 2. leetcode.vscode-leetcode 0.18.4 — HIGH (stale + credentials)
- Last release 2023; bundles 32 MB of node_modules with versions below published advisory fixes:
  underscore 1.9.1 (CVE-2021-23358), moment 2.29.3 (CVE-2022-31129), qs 6.5.2 (CVE-2022-24999),
  request 2.88.0 (CVE-2023-28155, deprecated), axios 1.6.8 (CVE-2024-39338), form-data 2.3.3 (CVE-2025-7783).
- Bundled vsc-leetcode-cli runs `eval()` on strings scraped from leetcode.com HTTP responses
  (node_modules/vsc-leetcode-cli/lib/plugins/leetcode.js:353,356).
- Live LeetCode session cookie (909 chars), JWT session id and CSRF token stored in plaintext at
  `~/.lc/leetcode/user.json` (login shiva2191, dated Feb 2025).
- Shipped `out/src/.env.local` contains only placeholder GA keys (not a leak).
- Status: already marked obsolete; folder, `~/.lc`, a `LeetCode.vscode-leetcode` state key and the
  `leetcode.workspaceFolder` setting remain. The configured workspace folder no longer exists.

### 3. mongodb.mongodb-vscode 1.18.0 — MEDIUM (official, broad surface)
- Verified publisher, signed gallery install. Activates on any JSON/JavaScript/plaintext file and on `onUri` handlers.
- Sends usage telemetry to api.segment.io by default (`mdb.sendTelemetry: true`) and fingerprints the machine via node-machine-id.
- Bundles a MongoDB MCP server and 7 chat skills exposed to Copilot Chat (`mdb.mcp.server: "prompt"`, `mdb.mcp.readOnly: true`).
- References to 169.254.169.254 / AWS come from the bundled AWS SDK (IAM auth), expected.
- Keep only if MongoDB is in active use; otherwise uninstall. If kept: set `mdb.sendTelemetry: false`, leave `mdb.mcp.readOnly: true`.

### 4. Microsoft Python/Jupyter extensions and Rainbow CSV — LOW
- All signed gallery installs from verified publishers, current versions.
- ms-python.python ships `sudo-prompt` (used to elevate for package installs when asked) — expected.
- Rainbow CSV `eval()` in rbql.js runs the user's own RBQL query — by design.
- ms-python.vscode-python-envs references thousands of pypi.org/anaconda.org URLs — package index metadata, expected.

### 5. Environment notes
- `terminal.integrated.inheritEnv: false` is set — good.
- `~/.vscode/argv.json` has `enable-crash-reporter: true` — optional to turn off.
- `mcp.json` runs `npx -y rea-agents@6.7.0` — pinned version, acceptable.
- Copilot extensions are obsolete because Copilot Chat is now built into VS Code; `globalStorage/github.copilot-chat`
  holds a 10 MB `settingEmbeddings.json` from Feb 2025 (harmless, reclaimable).

## Deletion plan (awaiting approval)

1. Quit VS Code.
2. Remove orphaned extension folders:
   `rm -rf ~/.vscode/extensions/heisebaiyun.kiro-for-cc-0.2.9 ~/.vscode/extensions/leetcode.vscode-leetcode-0.18.4`
3. Remove stored LeetCode credentials: `rm -rf ~/.lc`, then log out of all sessions at leetcode.com to invalidate the cookie.
4. Remove `leetcode.workspaceFolder` from `~/Library/Application Support/Code/User/settings.json`.
5. Clean `.obsolete` entries and the `heisebaiyun.kiro-for-cc` / `LeetCode.vscode-leetcode` keys from `state.vscdb` (after a backup).
6. MongoDB: either `code --uninstall-extension mongodb.mongodb-vscode` or add `"mdb.sendTelemetry": false` to settings.
7. Optional: set `enable-crash-reporter` to false in `~/.vscode/argv.json`; delete `globalStorage/github.copilot-chat/settingEmbeddings.json`.
8. Relaunch VS Code and verify with `code --list-extensions --show-versions`.

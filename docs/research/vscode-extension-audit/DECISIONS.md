# Decisions — VS Code extension audit

## 2026-10-20 — Remove orphaned kiro-for-cc and LeetCode extension folders plus LeetCode credentials
- **Decision:** Delete `heisebaiyun.kiro-for-cc-0.2.9` and `leetcode.vscode-leetcode-0.18.4` folders, `~/.lc/`, the
  `leetcode.workspaceFolder` setting and their `state.vscdb` keys.
- **Why:** kiro-for-cc launches Claude Code with permission prompts bypassed and is from an unverified publisher;
  LeetCode extension is stale (2023) with vulnerable bundled deps and stores a live session cookie in plaintext.
  Both were already uninstalled; only remnants remained.
- **Alternatives:** leave remnants (rejected: credentials on disk, orphan folders confuse future audits).

## 2026-10-20 — Keep MongoDB extension, disable telemetry
- **Decision:** Keep `mongodb.mongodb-vscode`, set `mdb.sendTelemetry: false`, leave `mdb.mcp.readOnly: true`.
- **Why:** Official verified publisher, in use; telemetry and machine fingerprinting are not needed.
- **Alternatives:** uninstall (rejected by user).

## 2026-10-20 — Hygiene
- **Decision:** Disable VS Code crash reporter in `~/.vscode/argv.json`; delete stale 10 MB Copilot `settingEmbeddings.json`.
- **Why:** Reduces outbound data; cache is regenerated on demand.

# Research notes — libraries and malicious-extension indicators (2026-10-20)

Compiled from official sources during planning. Re-verify a row when the task that uses it starts.

## Toolchain and libraries

| Item | Finding | Source |
|---|---|---|
| Go | Latest stable go1.27.3. `go 1.27.0` (minimum) + `toolchain go1.27.3` in go.mod; `GOTOOLCHAIN=auto` downloads it. Bubble Tea v2 and openai-go v3 require Go ≥ 1.26 | go.dev/dl, go.dev/doc/toolchain |
| Bubble Tea | `charm.land/bubbletea/v2` v2.1.0 (2026-10-08). v2 stable since 2026-02-24. Do not mix with v1 `github.com/charmbracelet/...` | github.com/charmbracelet/bubbletea, pkg.go.dev |
| Bubbles / Lip Gloss / Huh | `charm.land/bubbles/v2` 2.2.1, `charm.land/lipgloss/v2` 2.0.6, `charm.land/huh/v2` 2.0.3 | release pages |
| Cobra | v1.10.2 (2025-12-03) | github.com/spf13/cobra |
| Anthropic SDK | `github.com/anthropics/anthropic-sdk-go` v1.82.0. Structured outputs GA: `OutputConfig: anthropic.OutputConfigParam{Format: anthropic.JSONOutputFormatParam{Schema: ...}}` then unmarshal the text block. Forced `tool_choice` returns 400 on Opus 5.5 / Sonnet 5.5 / Fable 5.1 | platform.claude.com/docs structured-outputs |
| OpenAI SDK | `github.com/openai/openai-go/v3` v3.82.0, Go ≥ 1.26. Responses API with `ResponseFormatTextJSONSchemaConfigParam{Strict: true}` | github.com/openai/openai-go |
| Ollama | `POST /api/chat` with `format` = JSON schema and `stream:false`; `GET /api/tags`, `POST /api/pull` (stream), `GET /api/version`. Port 11434. OpenAI-compatible `/v1/chat/completions` is official but less documented for structured output | github.com/ollama/ollama/blob/main/docs/api.md, docs.ollama.com |
| Keychain | `zalando/go-keyring` v0.2.8: darwin impl shells out to `/usr/bin/security`, no cgo. `keybase/go-keychain` is cgo and makes the binary the ACL principal | repo sources |
| Trash | `/usr/bin/trash [-v] [-s] FILE...` "first appeared in macOS 15.0" (man page). Fallback: `osascript -e 'tell application "Finder" to delete POSIX file "..."'` | local man page |
| Fuzzy | `sahilm/fuzzy` v0.1.3 (2026-06) returns matched indexes for highlighting | pkg.go.dev |
| GoReleaser | v2.18.3. `brews` deprecated (hard since 2.16) → `homebrew_casks`. `notarize.macos` (quill) is OSS; `macos_native` is Pro | goreleaser.com/customization |

## Extension directories and manifests
- VS Code: `~/.vscode/extensions` (official). Folder name is `~/<product.json dataFolderName>/extensions`; Code-OSS/VSCodium use `.vscode-oss`. Insiders `~/.vscode-insiders`, Cursor `~/.cursor`, Windsurf `~/.windsurf` (community sources only).
- `extensions.json`: array of `{identifier:{id,uuid}, version, location:{path}, relativeLocation, metadata:{publisherId, publisherDisplayName, installedTimestamp, source, isBuiltin, ...}}`. `.obsolete` lists folders VS Code no longer loads.
- Marketplace: `POST https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery`, `Accept: application/json;api-version=3.0-preview.1`, body `{"filters":[{"criteria":[{"filterType":7,"value":"publisher.name"},{"filterType":8,"value":"Microsoft.VisualStudio.Code"}],"pageNumber":1,"pageSize":1}],"flags":912}`. Response has `publisher.isDomainVerified`, `publisher.flags`, `extension.flags`, `publishedDate`, `lastUpdated`, `statistics[]` (`install`, `averagerating`, ...). No auth.
- Open VSX: `GET https://open-vsx.org/api/{namespace}/{name}` → `verified`, `downloadCount`, `publishedWithTrustedPublishing`, `timestamp`, `deprecated`. Cursor and Windsurf pull from Open VSX.

## Statically checkable malicious-extension indicators (2024–2026)
1. Invisible Unicode payloads: runs of variation selectors U+FE00–FE0F / U+E0100–E01EF or Private Use Area code points decoded and eval'd (GlassWorm, late 2025).
2. `eval` / `new Function` / `child_process` with string-built or hex/base64-decoded arguments; heavy obfuscation; logic hidden among dead code (Checkmarx 2025, Material Theme 2025).
3. Remote second stage: fetch to hard-coded IP/URL → `chmod +x` → spawn (Checkmarx 2025; ReversingLabs `ahban.shiba` 2025).
4. Hard-coded IPs, non-TLS beacons, blockchain/DHT/calendar C2 (GlassWorm).
5. Credential-file access: `~/.npmrc`, `~/.gitconfig`, `~/.aws`, `~/.ssh`, `.env`, browser profiles, wallet paths; HTTP POST exfil (GlassWorm; Nx Console trojan 2026).
6. Overbroad activation: `activationEvents: ["*"]` or `onStartupFinished` where the stated purpose does not need it; cooldown timers in `globalState`.
7. Install-time hooks: `postinstall` / `preinstall` scripts, especially obfuscated (ExtensionTotal rule families).
8. Typosquatting / brand impersonation: edit distance ≤ 2 from popular ids, copied README/icon, `repository` pointing at another publisher (Aqua 2023, Checkmarx 2025).
9. Registry trust signals: unverified publisher, publisher age < 6 months, tiny or spiked installs, no repository, no trusted publishing on Open VSX (Microsoft Marketplace trust post 2025).
10. Capability mismatch: screenshot/clipboard/keylog/mining code or native `.node` binaries in a theme or chat extension (MaliciousCorgi 2026, Bitcoin Black 2025).

Plus from the local audit (`vscode-extension-audit/REPORT.md`): orphaned folders left after uninstall, `.obsolete` and `state.vscdb` remnants, plaintext session tokens under `~`, stale bundled deps with CVEs, AI-agent wrappers that launch tools with permission bypass flags.

## Not verified
Fork extension folder names (community sources); Apple `trashItemAtURL` availability; whether quill-based `notarize.macos` is formally OSS (inferred from only `macos_native` being labelled Pro).

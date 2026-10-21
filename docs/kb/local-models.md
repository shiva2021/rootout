# Local models (Ollama)

Code: `internal/llm/ollama` (catalogue in `catalog.go`). Decision: D-009.
Verified 2026-10-20 against the pages listed at the bottom.

## How a model is judged
- Resident size ≈ download size × 1.2. ≤ 60 % of RAM = comfortable, ≤ 80 % = tight, else too large.
- Tier = largest RAM class the Mac meets (8 / 16 / 32 / 64 GB). Recommend lists that tier and the ones below, best first; larger tiers follow as "too large".
- Intel Macs: capped at the 8 GB tier, every reason prefixed "CPU-only, slow: ".
- Sizes are the default tag's download size on ollama.com (decimal GB). Where the page shows a range, the upper bound is used.

## Catalogue

| Tier | Tag | Download | Why |
|---|---|---|---|
| 8 GB | `qwen3.5:4b` | 3.3–4.0 GB | Newest small Qwen; strong code reading and JSON. Thinking model: send `think: false` for speed. |
| 8 GB | `qwen2.5-coder:3b` | 1.9 GB | Code-specialised and light; reliable JSON. |
| 8 GB | `llama3.2:3b` | 2.0 GB | Well-tested general model; fallback. |
| 16 GB | `qwen3.5:9b` | 6.6–7.6 GB | Best all-round mid-size; good code analysis and structured output. |
| 16 GB | `qwen2.5-coder:7b` | 4.7 GB | Code-specialised 7B; fast on JS/TS. |
| 16 GB | `qwen3:8b` | 5.2 GB | Solid general 8B with tools/JSON. |
| 32 GB | `qwen2.5-coder:14b` | 9.0 GB | Code-specialised 14B. |
| 32 GB | `qwen3:14b` | 9.3 GB | Strong instruction following and JSON. |
| 32 GB | `gemma4:12b` | 7.7–8.0 GB | Current Gemma; long context. |
| 64 GB | `qwen3-coder:30b` | 19 GB | Current Qwen coder; MoE (3B active), fast for its size. |
| 64 GB | `qwen3.5:27b` | 17–20 GB | Large dense Qwen; top quality. |
| 64 GB | `qwen2.5-coder:32b` | 20 GB | Proven 32B coder; very reliable JSON. |

Considered and left out: `llama3.1:8b` (older, superseded by qwen3/qwen3.5 for JSON), `gemma4:e4b` (6.6–9.5 GB, too big for 8 GB despite "4b"), `devstral-small-2:24b` (agentic coding focus, larger than needed).

## API used
- `GET /api/version` → `{"version": "..."}`.
- `GET /api/tags` → `models[].name, size, details.{family, parameter_size, quantization_level}`.
- `POST /api/pull {"model", "stream": true}` → NDJSON lines `{status, digest, total, completed}` (`completed` absent until bytes arrive), ending `{"status":"success"}`. A mid-stream failure is a line `{"error": "..."}` with HTTP 200 already sent, so every line is checked for `error`.
- `DELETE /api/delete {"model"}` → 200 with no body; a missing model is 404 `{"error": "model '…' not found"}`, mapped to `ErrModelNotFound`.
- Model names must match `^[a-zA-Z0-9._:/-]+$` before a pull or delete.

## Uninstalling a model
- Picker: `d` on an installed row (either section) asks `Uninstall <name> (<size>)? y/n`, deletes with a spinner, re-lists, and flashes the result. Recommended rows show `installed` / `not installed`.
- CLI: `rootout config models rm <model> [--yes]`.
- If the model was the configured default, the setting is cleared (`ModelDeps.ClearDefault` in the picker, `removeModel` in the CLI) so scans do not ask for a model that is gone. Decision D-025.

## Sources (checked 2026-10-20)
- https://ollama.com/library
- https://ollama.com/library/qwen3.5/tags, /qwen2.5-coder/tags, /qwen3/tags, /qwen3-coder/tags, /llama3.2/tags, /gemma4/tags
- https://github.com/ollama/ollama/blob/main/docs/api.md (moving to https://docs.ollama.com/api)
- https://docs.ollama.com/api/errors (mid-stream error shape)
- https://docs.ollama.com/api/delete (checked 2026-10-21)

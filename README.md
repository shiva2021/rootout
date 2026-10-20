# purge

Uninstall macOS apps completely, and find risky extensions in VS Code, Cursor,
VSCodium, Windsurf and VS Code Insiders.

```bash
purge                       # pick an app, review everything it left behind, remove it
purge list [--search q]     # installed apps
purge remove <app> -n       # dry run: show the bundle and every leftover file
purge remove <app>          # move them to the Trash after you confirm
purge scan                  # score installed extensions; review and uninstall on a terminal
purge scan --llm            # also ask Claude, OpenAI or a local Ollama model
purge config models --pick  # choose or download a local model that fits this Mac
purge doctor                # check what purge depends on
```

Every command takes `--json`. Removal goes to the Trash unless `--permanent`.
System apps are listed but never removable. Nothing runs as root; files that
need admin rights are removed in one step after a single macOS password prompt.

Extension scanning is static: extension code is never executed. Scores separate
what an extension can do (spawn processes, eval) from evidence of intent
(hidden payloads, credential reads, exfiltration hosts); see
`docs/kb/heuristics.md`. An AI model, if configured, sees only the findings and
short snippets, never the whole extension.

Development: see `CLAUDE.md` and `docs/kb/README.md`.

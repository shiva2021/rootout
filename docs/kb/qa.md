# Manual QA: real-app removal

Automated tests never touch the real home. Before any release, run this on a
**throwaway macOS user account** (System Settings > Users & Groups > Add User),
never on the main account.

## Setup (once per QA account)
1. Log into the QA account. Install three apps of different origins:
   - a Homebrew cask (`brew install --cask ghostty`),
   - a Mac App Store app (any free one),
   - a pkg-installed app that ships a LaunchAgent or privileged helper (e.g. a printer utility).
2. Launch each app once so it writes preferences, caches and saved state.
3. Build rootout from the main account and copy `bin/rootout` over, or `make build` in the QA account.

## Checklist per app
```bash
rootout list --json | jq '.[] | select(.name=="<App>")'
rootout remove "<App>" --dry-run            # inspect every path and its MatchedBy
rootout remove "<App>"                       # confirm in the prompt
find ~/Library /Library -iname '*<bundle-id>*' 2>/dev/null   # expect nothing
ls ~/.Trash                                # bundle + leftovers present
```
Record for each app: number of leftovers found, any path that was *missed*
(found by `find` afterwards), any path that looked wrong in the dry run, and
whether the admin prompt appeared exactly once when root-owned items existed.

## Negative checks (must refuse)
```bash
rootout remove Mail      # protected: com.apple.*
rootout remove Safari
rootout remove Finder
```

## Results log
| Date | Build | App | Origin | Leftovers found | Missed | Wrong | Notes |
|---|---|---|---|---|---|---|---|

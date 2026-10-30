# Releases

Pushing a `v*` tag publishes rootout. The `Release` workflow runs GoReleaser,
builds darwin arm64 and amd64, attaches the archives and `checksums.txt` to a
GitHub Release whose notes come from `CHANGELOG.md`, and updates the private
tap `shiva2021/homebrew-rootout`. The `CI` workflow runs test and lint on every
push to `main` and every pull request. Decisions: D-030, D-031, D-032.

## Cut a release
- Move the Unreleased items under a new `## [X.Y.Z] - YYYY-MM-DD` heading in `CHANGELOG.md` and commit.
- `make release-check NEXT=vX.Y.Z`. It checks the CHANGELOG section first, then runs test, lint and `goreleaser check`. Always run it: the release job does not run the tests.
- `git tag -a vX.Y.Z -m "vX.Y.Z: <one line>"`
- `git push origin main vX.Y.Z`
- Watch the `Release` workflow: `gh run list --workflow Release --limit 1`, then `gh run watch --exit-status <id>`.
- Check the release page (two archives, `checksums.txt`, the CHANGELOG notes) and that `Casks/rootout.rb` in `homebrew-rootout` moved to the new version.
- To try the pipeline without publishing: `make snapshot` builds both archives and the cask into `dist/`.

## Install from the tap
You need git access to the tap (SSH key on GitHub, or `gh auth setup-git`) and a token with read access to `shiva2021/rootout` releases.
```bash
export HOMEBREW_GITHUB_API_TOKEN=<token with read access to shiva2021/rootout>
brew tap shiva2021/rootout git@github.com:shiva2021/homebrew-rootout.git
brew trust shiva2021/rootout   # newer Homebrew refuses casks from untrusted taps
brew install --cask rootout
brew upgrade --cask rootout   # later, for a new version (token still exported)
```

## Which binary am I running
- `which -a rootout` lists every copy on `PATH`; the first one wins. `rootout --version` says which version it is.
- Tap install (`/opt/homebrew/bin/rootout`) for daily use.
- `make install` (`~/.local/bin/rootout`) only for testing a branch.
- Never keep both on `PATH`: `~/.local/bin` often comes first and hides the brew copy. Remove one with `rm ~/.local/bin/rootout` or `brew uninstall --cask rootout`.

## Repair a failed release
- Never move or delete a tag.
- Fix the cause on `main`, add a CHANGELOG entry for the next patch version, run `make release-check`, and cut the patch tag.
- A tag with no CHANGELOG section (or an empty one) fails before GoReleaser runs; that is intended.
- If GoReleaser left a half-made GitHub Release behind, delete that release by hand on GitHub (keep the tag).
- `homebrew cask: could not get default branch: ... homebrew-rootout: 404 Not Found` means `TAP_GITHUB_TOKEN` cannot see the tap: the token's repository access does not include `homebrew-rootout` (or its owner is not shiva2021). The GitHub Release is already published at that point; fix the token, then cut a patch tag so the cask is written (happened on v0.4.0).

## Tokens
- `TAP_GITHUB_TOKEN`: Actions secret on `shiva2021/rootout`. Fine-grained, resource owner shiva2021, repository `homebrew-rootout` only, Contents read and write, one-year expiry.
- Rotate it before it expires: create a new token with the same settings, then `gh secret set TAP_GITHUB_TOKEN --repo shiva2021/rootout` and paste it. Delete the old token.
- `HOMEBREW_GITHUB_API_TOKEN`: each user's own token, read access to `shiva2021/rootout` releases. Needed for `brew install` and `brew upgrade`.
- On a machine with `gh` logged in, `export HOMEBREW_GITHUB_API_TOKEN=$(gh auth token)` is enough (its OAuth token has `repo` scope; verified with v0.4.1).

## Known limits
- The binary is not code-signed or notarized (D-030). The cask clears the quarantine flag after install; a direct download needs `xattr -d com.apple.quarantine rootout`. A future macOS may block this; signing is the fix.
- The tap is private, so `brew tap` needs git credentials. Over HTTPS it prompts; the SSH URL avoids that.
- The cask's download strategy calls Homebrew's internal `GitHub.get_release` (works as of v0.4.1), which may change. Fallback: fetch `https://api.github.com/repos/<owner>/<repo>/releases/tags/<tag>` in the same class and pick the asset id from that (D-031).

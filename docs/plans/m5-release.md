# M5 · Release pipeline

_Spec written 2026-10-22. Status: awaiting review. Decisions D-030…D-032 are logged once this is approved._

## Goal

Pushing a `v*` tag publishes rootout: GitHub Actions builds darwin arm64 and
amd64 with GoReleaser, attaches the archives and checksums to a GitHub
Release whose notes come from `CHANGELOG.md`, and updates a private Homebrew
tap so `brew install --cask rootout` works for anyone holding a GitHub token.
A second workflow runs `make test` and `make lint` on every push and pull
request. The binary ships unsigned.

Out of scope: signing and notarization (deferred, D-030), a public repo, a
`LICENSE` file (M6 decides the terms), the membership gate (M6), Linux or
Windows builds.

## What already exists

- `Makefile` builds with `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)"`; `make install` copies to `~/.local/bin`.
- `cmd/rootout/main.go` has `var version = "dev"`; `rootout --version` prints `rootout version v0.3.1`, `doctor` and the home title show the same string.
- Annotated semver tag on every merge (D-028); `CHANGELOG.md` in Keep a Changelog format with a `## [X.Y.Z] - date` heading per version.
- Repo `shiva2021/rootout` is private. No `.github/`, no `.goreleaser.yaml`, GoReleaser not installed locally.
- Eight files carry `//go:build darwin` / `other` tags (`internal/app`, `internal/plan`, `internal/safety`).

## Design

### Files

| Path | Role |
|---|---|
| `.goreleaser.yaml` | builds, archives, checksum, release, homebrew_casks, snapshot; changelog disabled |
| `.github/workflows/ci.yml` | test and lint on push to `main` and on pull requests |
| `.github/workflows/release.yml` | on tag `v*`: release notes, GoReleaser release |
| `scripts/release_notes.sh` | print one version's `CHANGELOG.md` section, exit 1 if absent |
| `Makefile` | new targets `release-check`, `snapshot` |
| `docs/kb/release.md` | runbook: cut, verify, repair a release; rotate the tap token |
| `README.md` | Install section (tap + direct download) |
| `docs/DECISIONS.md`, `docs/kb/setup.md`, `docs/kb/conventions.md`, `docs/kb/README.md`, `docs/PLAN.md`, `CHANGELOG.md`, `docs/HANDOVER.md` | updated |

### GoReleaser configuration (`version: 2`, GoReleaser 2.18.3)

- **builds**: one build, `main: ./cmd/rootout`, `env: [CGO_ENABLED=0]`,
  `goos: [darwin]`, `goarch: [amd64, arm64]`, `flags: [-trimpath]`,
  `ldflags: [-s -w -X main.version=v{{ .Version }}]`, `mod_timestamp: "{{ .CommitTimestamp }}"`.
  `v{{ .Version }}` gives `v0.4.0` for a release (same string as a local
  tagged build) and `v0.3.2-next` for a snapshot, so a snapshot binary is
  never mistaken for the tagged one.
- **archives**: `tar.gz`, `name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`,
  files `README.md` and `CHANGELOG.md`.
- **checksum**: `checksums.txt`.
- **changelog**: `disable: true`. Notes are supplied with `--release-notes`.
- **release**: `github: {owner: shiva2021, name: rootout}`, `prerelease: auto`.
- **snapshot**: `version_template: "{{ incpatch .Version }}-next"`.
- **homebrew_casks** (one entry, `name: rootout`):
  - `repository: {owner: shiva2021, name: homebrew-rootout, token: "{{ .Env.TAP_GITHUB_TOKEN }}"}`; `directory: Casks`; `commit_author` Shivang Chaturvedi; `skip_upload: auto`.
  - `homepage`, `description`, `binaries: [rootout]`.
  - `url.template: "https://github.com/shiva2021/rootout/releases/download/{{ .Tag }}/{{ .ArtifactName }}"` and `url.using: GitHubPrivateRepositoryReleaseDownloadStrategy`.
  - `custom_block`: the `CurlDownloadStrategy` subclass from the GoReleaser
    docs (fetched 2026-10-22): parses the release URL, reads
    `HOMEBREW_GITHUB_API_TOKEN` at download time (Homebrew ≥ 5.1.14 scrubs it
    during cask evaluation), resolves the asset id through the releases API
    and downloads `releases/assets/<id>` with `Accept: application/octet-stream`.
  - `hooks.post.install_steps`: `on_macos do run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{ .StagedPath }}/rootout"] end`.
  - `caveats`: export `HOMEBREW_GITHUB_API_TOKEN` before `brew install`; the binary is unsigned.

### Workflows

**ci.yml** — `on: push: branches: [main]`, `pull_request`. One job on
`macos-latest` (the darwin build tags mean Linux would test the wrong code
paths): `actions/checkout@v7.0.1`, `actions/setup-go@v7.0.0` with
`go-version-file: go.mod` and `cache: true`, `make test`, then
`golangci/golangci-lint-action@v9.3.0` with `version: v2.15.0` and a `gofmt -l`
check (same as `make lint`). Concurrency group per ref, cancel in progress.

**release.yml** — `on: push: tags: ["v*"]`, `permissions: contents: write`.
One job on `ubuntu-latest` (cross-compiling darwin needs no cgo and costs
1x minutes): checkout with `fetch-depth: 0`, setup-go from `go.mod`,
`scripts/release_notes.sh "$GITHUB_REF_NAME" > "$RUNNER_TEMP/notes.md"`,
then `goreleaser/goreleaser-action@v7.3.0` with `version: v2.18.3` and
`args: release --clean --release-notes ${{ runner.temp }}/notes.md`. Env:
`GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`, `TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}`.

All action and tool versions pinned exactly (verified against GitHub
releases on 2026-10-22). Go comes from the `go.mod` pin, not a workflow string.

### Release notes script

`scripts/release_notes.sh vX.Y.Z` (or `X.Y.Z`) prints the body of the
`## [X.Y.Z]` section of `CHANGELOG.md`, from the heading to the next `## `
heading, trimmed. It exits 1 with a message if no such heading exists. The
release workflow therefore fails when a tag has no changelog entry. Repair:
add the entry and cut a patch tag. Tags are never moved or deleted.

### Makefile

- `release-check`: `make test`, `make lint`, `goreleaser check`, then
  `scripts/release_notes.sh "$(NEXT)" >/dev/null` where `NEXT` is given on the
  command line (`make release-check NEXT=v0.4.0`). This is the test gate for a
  release: `release.yml` does not run the tests (Ubuntu would exercise the
  wrong build tags, macOS costs 10x), and tag and `main` are pushed together,
  so tagging goes through `release-check` first. The runbook says so.
- `snapshot`: `goreleaser release --snapshot --clean --skip=publish`; builds
  both archives and the cask into `dist/` without touching GitHub. The target
  sets `TAP_GITHUB_TOKEN ?= snapshot` so the cask's `{{ .Env.TAP_GITHUB_TOKEN }}`
  template renders offline; drop the guard if step 1 of Verification shows
  GoReleaser does not need it.

### Tap and secrets (manual, Shivang)

1. `gh repo create shiva2021/homebrew-rootout --private` with a one-line README.
2. Fine-grained personal access token: resource owner shiva2021, repository
   access `homebrew-rootout` only, permission Contents read and write, one
   year expiry. Saved as Actions secret `TAP_GITHUB_TOKEN` on `shiva2021/rootout`.
3. A user installing needs two credentials: git access to the private tap
   (SSH key or `gh auth setup-git`) and `HOMEBREW_GITHUB_API_TOKEN` with
   read access to `shiva2021/rootout` releases. Documented in
   `docs/kb/release.md` and the README.

### Install story (README)

```bash
export HOMEBREW_GITHUB_API_TOKEN=<token with read access to shiva2021/rootout>
brew tap shiva2021/rootout git@github.com:shiva2021/homebrew-rootout.git
brew install --cask rootout
```

Direct download: fetch the archive from the release page, `tar xzf`, then
`xattr -d com.apple.quarantine rootout` and move it onto `PATH`.
`make install` keeps working for development.

### Interaction with `make install`

Both put a `rootout` on `PATH`. After the tap install is verified, remove
`~/.local/bin/rootout` so the two cannot shadow each other; the runbook says
which to use when (tap for daily use, `make install` for a branch under test).

## Verification

Done in this order; each step must pass before the next.

1. `brew install goreleaser` (2.18.3, recorded in `docs/kb/setup.md`); `make release-check NEXT=v0.3.1` passes against the existing changelog.
2. `make snapshot`: `dist/` holds two archives and `checksums.txt`; the arm64 binary prints `rootout version v0.3.2-next` (snapshot template); `dist/homebrew/Casks/rootout.rb` contains the strategy class, the xattr hook and the right URL.
3. `make test` and `make lint` still green locally.
4. Push the workflows on `main`; `ci.yml` green in Actions.
5. Create the tap repo and the secret; add the `0.4.0` CHANGELOG entry; tag `v0.4.0`; `release.yml` green; the GitHub Release has two archives, `checksums.txt` and the CHANGELOG notes; `homebrew-rootout` has `Casks/rootout.rb`.
6. On this Mac: tap and install per the README; `rootout --version` prints `rootout version v0.4.0`; `rootout doctor` runs; `brew uninstall --cask rootout` removes it; reinstall for daily use and remove `~/.local/bin/rootout`.
7. `scripts/toolchain_check.sh` still clean.

## Risks

- GoReleaser's documented strategy calls Homebrew's internal `GitHub.get_release`, which the docs say may need replacing. Only step 6 exercises it. Fallback: resolve the asset id with `Utils::Curl` against `https://api.github.com/repos/<o>/<r>/releases/tags/<tag>` inside the same class.
- `brew tap` of a private repo over HTTPS prompts for credentials; the SSH URL form avoids that but needs an SSH key on GitHub.
- macOS runner minutes bill at 10x on a private repo. CI is about two minutes a push, so roughly 20 billed minutes each, within the 2000 free minutes a month at the current pace.
- An unsigned binary depends on the xattr hook, which Apple may disable in a future macOS. Signing (D-015 flow) is the fix and becomes a later task.
- A tag without a changelog entry fails the release. Intended, and caught locally by `make release-check`.

## Decisions to log on approval

- **D-030** Signing and notarization deferred; unsigned binary with a quarantine-clearing cask hook. Supersedes the "needs a paid account before M5" consequence in D-015.
- **D-031** Private repo and private tap; cask downloads through GoReleaser's private-release strategy with `HOMEBREW_GITHUB_API_TOKEN`.
- **D-032** Release on every `v*` tag; notes from `CHANGELOG.md`, missing entry fails the release; CI tests on macOS, release cross-compiles on Ubuntu without re-running tests, so `make release-check` (test, lint, config, changelog) is the gate before tagging; actions pinned to exact versions.

---

# M5 Release Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pushing a `v*` tag builds darwin arm64/amd64 archives with GoReleaser, publishes a GitHub Release with CHANGELOG notes, and updates the private Homebrew tap `shiva2021/homebrew-rootout`.

**Architecture:** One `.goreleaser.yaml` holds build, archive, release and cask config. Two GitHub Actions workflows: `ci.yml` (test + lint on macOS) and `release.yml` (GoReleaser on Ubuntu, triggered by tags). A shell script extracts release notes from `CHANGELOG.md`; two Makefile targets (`release-check`, `snapshot`) make the pipeline testable before any tag is pushed.

**Tech Stack:** Go 1.27 (from `go.mod`), GoReleaser v2.18.3, GitHub Actions (`actions/checkout@v7.0.1`, `actions/setup-go@v7.0.0`, `golangci/golangci-lint-action@v9.3.0` with golangci-lint v2.15.0, `goreleaser/goreleaser-action@v7.3.0`), Homebrew cask DSL, bash.

**Spec:** the first half of this file (`docs/plans/m5-release.md`, sections Goal through Decisions to log).

**Step 0 (before Task 1, playbook rule "log the decisions, then build"):** append D-030, D-031, D-032 to `docs/DECISIONS.md` (texts in the spec's "Decisions to log on approval"; date 2026-10-22; D-030 Status `Accepted (supersedes the "needs a paid Apple Developer account before M5" consequence in D-015; the quill flow stays the way to sign later)`; alternatives: D-031 public repo and tap, no tap; D-032 manual dispatch, GoReleaser commit changelog, tests in the release job). Then `git add docs/plans/m5-release.md docs/DECISIONS.md && git commit -m "docs: M5 release pipeline spec, plan and D-030…D-032"`.

## Global Constraints

- `CGO_ENABLED=0`, `-trimpath`, `-s -w`, version injected as `-X main.version=v{{ .Version }}`.
- Every action, GoReleaser and golangci-lint version pinned exactly; Go comes from `go-version-file: go.mod`.
- Tags are never moved or deleted; a failed release is repaired with a new patch tag.
- No signing, no `LICENSE`, no public repo in this milestone.
- Commits follow `docs/kb/conventions.md`: imperative, under 72 chars, `area: summary`, decisions referenced as `D-0NN`.
- No test touches the real home directory; the script test uses `t.TempDir()`.

## Review Focus

1. `scripts/release_notes.sh` given `v0.4.0` and `0.4.0` must print the same section; given `0.4` or `0.4.01` it must not match `[0.4.0]`. (Task 1 test cases `v-prefix`, `bare`, `no-partial-match`, `no-prefix-match`.)
2. The last version section in `CHANGELOG.md` has no following `## ` heading; the script must print it to end of file. (Task 1 case `last-section`.)
3. A heading with an empty body must exit 1, otherwise a release ships with blank notes. (Task 1 case `empty-section`.)
4. `make snapshot` with `TAP_GITHUB_TOKEN` unset must still render the cask; the Makefile default covers it, and Task 2 step 6 confirms whether GoReleaser needed it.
5. After the tap install, `~/.local/bin/rootout` from `make install` shadows the brew binary when `~/.local/bin` precedes `/opt/homebrew/bin` on `PATH`. Task 6 removes it and the runbook explains which to use.

---

### Task 1: Release notes script

**Files:**
- Create: `scripts/release_notes.sh`
- Test: `cmd/rootout/release_notes_test.go`

**Interfaces:**
- Produces: `scripts/release_notes.sh <version> [changelog]`. `<version>` is `vX.Y.Z` or `X.Y.Z`; `[changelog]` defaults to `CHANGELOG.md` relative to the current directory. Prints the body of the `## [X.Y.Z]` section (lines after the heading up to, not including, the next line starting with `## `, with leading and trailing blank lines removed) to stdout, exit 0. Exit 1 with `release_notes: no CHANGELOG section for X.Y.Z` or `release_notes: empty CHANGELOG section for X.Y.Z` on stderr. Exit 2 on a missing argument or unreadable file.

- [ ] **Step 1: Write the failing test**

`cmd/rootout/release_notes_test.go`, package `main`, stdlib only. Helper `runReleaseNotes(t, version, changelog string) (stdout string, exitCode int)` that writes `changelog` to `filepath.Join(t.TempDir(), "CHANGELOG.md")` and runs `exec.Command("bash", "../../scripts/release_notes.sh", version, path)`. Table cases:

| name | version | changelog | want stdout | want exit |
|---|---|---|---|---|
| `v-prefix` | `v0.4.0` | `## [Unreleased]\n\n## [0.4.0] - 2026-10-22\n\n### Added\n- Release pipeline (D-032).\n\n## [0.3.1] - 2026-10-22\n\n- old\n` | `### Added\n- Release pipeline (D-032).` | 0 |
| `bare` | `0.4.0` | same | same | 0 |
| `last-section` | `0.3.1` | same | `- old` | 0 |
| `no-partial-match` | `0.4` | same | `` | 1 |
| `no-prefix-match` | `0.4.01` | same | `` | 1 |
| `missing` | `9.9.9` | same | `` | 1 |
| `empty-section` | `0.5.0` | `## [0.5.0] - 2026-10-23\n\n## [0.4.0] - 2026-10-22\n\n- x\n` | `` | 1 |

Assert stdout after `strings.TrimRight(.., "\n")` equals want and the exit code matches (`exec.ExitError.ExitCode()`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/rootout -run TestReleaseNotes -v`
Expected: FAIL, every case, because the script does not exist (`bash: ../../scripts/release_notes.sh: No such file`).

- [ ] **Step 3: Write `scripts/release_notes.sh`**

`#!/usr/bin/env bash`, `set -euo pipefail`. Strip a leading `v` from `$1`; `awk` with the version passed as `-v ver=`, matching `^## \[` ver `\]` with the dots escaped (build the pattern with `gsub(/\./, "\\.", ver)` in a `BEGIN` block, or use `index($0, "## [" ver "]") == 1` which needs no escaping and is the simpler choice). Collect lines until the next `/^## /`. Trim blank lines at both ends in the shell (or awk). Emit the two error messages and exit codes from the Interfaces block. `chmod +x`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/rootout -run TestReleaseNotes -v`
Expected: PASS, seven subtests. Also run against the real file: `scripts/release_notes.sh v0.3.1` prints the `### Changed` block from `CHANGELOG.md`; `scripts/release_notes.sh v9.9.9; echo $?` prints the error and `1`.

- [ ] **Step 5: Lint and commit**

Run: `make lint`. Expected: clean. If gosec reports G204 (subprocess with variable arguments) on the `exec.Command` in the test, add `G204` to the existing test-file exclusion in `.golangci.yml` next to G302 (repo precedent, handover gotcha: exclude in config, never `//nolint` inline).
```bash
git add scripts/release_notes.sh cmd/rootout/release_notes_test.go
git commit -m "scripts: release_notes.sh extracts one CHANGELOG section (D-032)"
```

---

### Task 2: GoReleaser configuration and Makefile targets

**Files:**
- Create: `.goreleaser.yaml`
- Modify: `Makefile` (`.PHONY` line, two new targets at the end)
- Modify: `docs/kb/setup.md` (Prerequisites: add GoReleaser)

**Interfaces:**
- Consumes: `scripts/release_notes.sh` (Task 1).
- Produces: `make release-check NEXT=vX.Y.Z` (exit 0 = safe to tag), `make snapshot` (writes `dist/`). Archive names `rootout_<ver>_darwin_<arch>.tar.gz`, `checksums.txt`; cask somewhere under `dist/` (located with `find dist -name rootout.rb`; path confirmed in step 6).

- [ ] **Step 1: Install GoReleaser locally**

Run: `brew install goreleaser && goreleaser --version`
Expected: `2.18.3` (if Homebrew ships a newer 2.x, record that version in setup.md and keep the CI pin at v2.18.3; `goreleaser check` must pass on both).

- [ ] **Step 2: Write `.goreleaser.yaml`**

```yaml
version: 2
project_name: rootout

before:
  hooks:
    - go mod download

builds:
  - id: rootout
    main: ./cmd/rootout
    env: [CGO_ENABLED=0]
    goos: [darwin]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags: ["-s -w -X main.version=v{{ .Version }}"]
    mod_timestamp: "{{ .CommitTimestamp }}"

archives:
  - formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files: [README.md, CHANGELOG.md]

checksum:
  name_template: checksums.txt

changelog:
  disable: true

snapshot:
  version_template: "{{ incpatch .Version }}-next"

release:
  github:
    owner: shiva2021
    name: rootout
  prerelease: auto

homebrew_casks:
  - name: rootout
    binaries: [rootout]
    homepage: https://github.com/shiva2021/rootout
    description: Uninstall macOS apps completely and scan VS Code-family extensions for malicious behaviour
    directory: Casks
    skip_upload: auto
    repository:
      owner: shiva2021
      name: homebrew-rootout
      token: "{{ .Env.TAP_GITHUB_TOKEN }}"
    commit_author:
      name: Shivang Chaturvedi
      email: shiva.chaturvedi91@gmail.com
    caveats: |
      rootout is distributed from a private GitHub repository and is not code-signed.
      Set HOMEBREW_GITHUB_API_TOKEN to a token with read access to shiva2021/rootout
      before `brew install`/`brew upgrade`. The quarantine attribute is cleared on install.
    url:
      template: "https://github.com/shiva2021/rootout/releases/download/{{ .Tag }}/{{ .ArtifactName }}"
      using: GitHubPrivateRepositoryReleaseDownloadStrategy
    custom_block: |
      require "download_strategy"

      class GitHubPrivateRepositoryReleaseDownloadStrategy < CurlDownloadStrategy
        def initialize(url, name, version, **meta)
          @url = url
          parse_url_pattern
          set_github_token
          meta[:headers] ||= []
          meta[:headers] << "Accept: application/octet-stream"
          meta[:headers] << "Authorization: Bearer #{@github_token}"
          ghurl = "https://api.github.com/repos/#{@owner}/#{@repo}/releases/assets/#{asset_id}"
          super(ghurl, name, version, **meta)
        end

        def parse_url_pattern
          url_pattern = %r{https://github.com/([^/]+)/([^/]+)/releases/download/([^/]+)/(\S+)}
          raise CurlDownloadStrategyError, "Invalid url pattern" unless @url =~ url_pattern

          _, @owner, @repo, @tag, @filename = *@url.match(url_pattern)
        end

        def download_url
          "https://#{@github_token}@api.github.com/repos/#{@owner}/#{@repo}/releases/assets/#{asset_id}"
        end

        private

        def set_github_token
          @github_token = ENV["HOMEBREW_GITHUB_API_TOKEN"]
          raise CurlDownloadStrategyError, "HOMEBREW_GITHUB_API_TOKEN is required." unless @github_token

          # validate access
          GitHub.repository(@owner, @repo)
        end

        def asset_id
          @asset_id ||= GitHub.get_release(@owner, @repo, @tag)["assets"]
                              .find { |a| a["name"] == @filename }
                              .fetch("id") { raise CurlDownloadStrategyError, "Asset not found" }
        end
      end
    hooks:
      post:
        install_steps: |
          on_macos do
            run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{ .StagedPath }}/rootout"]
          end
```

The `custom_block` is verbatim from the GoReleaser `homebrew_casks` docs ("Private repositories", fetched 2026-10-22). Key names must match GoReleaser 2.18 docs; if `goreleaser check` rejects one (for example `archives.formats` vs `format`), fix to the documented name and note it in the commit body.

- [ ] **Step 3: Add Makefile targets**

Append to `.PHONY`: `release-check snapshot`. Targets:

```make
# release-check is the gate before tagging: tests, lint, GoReleaser config, and a CHANGELOG
# section for the version about to be tagged. Usage: make release-check NEXT=v0.4.0
release-check:
	@test -n "$(NEXT)" || { echo "usage: make release-check NEXT=vX.Y.Z"; exit 2; }
	scripts/release_notes.sh "$(NEXT)" >/dev/null && echo "CHANGELOG has $(NEXT)"
	$(MAKE) test lint
	goreleaser check

# snapshot builds both archives and the cask into dist/ without publishing.
TAP_GITHUB_TOKEN ?= snapshot
export TAP_GITHUB_TOKEN
snapshot:
	goreleaser release --snapshot --clean --skip=publish
```

- [ ] **Step 4: Validate the config**

Run: `goreleaser check`
Expected: `1 configuration file(s) validated` with no deprecation warnings. Fix any reported key and re-run.

- [ ] **Step 5: Run the release gate against the current tag**

Run: `make release-check NEXT=v0.3.1`
Expected: `CHANGELOG has v0.3.1`, then tests and lint pass, then `goreleaser check` passes. `make release-check` with no `NEXT` exits 2 with the usage line in under a second, before any test runs.

- [ ] **Step 6: Build a snapshot and inspect it**

Run: `make snapshot && ls dist/ && tar tzf dist/rootout_*_darwin_arm64.tar.gz && "$(find dist -type f -name rootout -path '*arm64*' | head -1)" --version && grep -c 'GitHubPrivateRepositoryReleaseDownloadStrategy\|com.apple.quarantine\|releases/download' "$(find dist -name rootout.rb)"`
Expected: `rootout_0.3.2-next_darwin_arm64.tar.gz`, `..._amd64.tar.gz`, `checksums.txt`; the archive lists `rootout`, `README.md`, `CHANGELOG.md`; the binary prints `rootout version v0.3.2-next`; exactly one `rootout.rb` under `dist/` (record its path in the Interfaces block above) and the grep count is at least 3. Then run `env -u TAP_GITHUB_TOKEN goreleaser release --snapshot --clean --skip=publish`: if it succeeds, delete the `TAP_GITHUB_TOKEN ?= snapshot` / `export` lines from the Makefile and say so in the commit body; if it fails on the missing env key, keep them.

- [ ] **Step 7: Record GoReleaser in setup.md and commit**

Add to `docs/kb/setup.md` Prerequisites: `- GoReleaser 2.18.3 (`brew install goreleaser`) for `make snapshot` and `make release-check`; CI pins the same version.`
```bash
git add .goreleaser.yaml Makefile docs/kb/setup.md
git commit -m "release: GoReleaser config, snapshot and release-check targets (D-030, D-031)"
```

---

### Task 3: GitHub Actions workflows

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `scripts/release_notes.sh` (Task 1), `.goreleaser.yaml` (Task 2), repo secret `TAP_GITHUB_TOKEN` (Task 6, created by Shivang).
- Produces: workflow names `CI` and `Release`, referenced by the runbook.

- [ ] **Step 1: Write `ci.yml`**

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v7.0.1
      - uses: actions/setup-go@v7.0.0
        with:
          go-version-file: go.mod
          cache: true
      - run: make test
      - run: test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
      - run: go vet ./...
      - uses: golangci/golangci-lint-action@v9.3.0
        with:
          version: v2.15.0
```

- [ ] **Step 2: Write `release.yml`**

```yaml
name: Release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v7.0.0
        with:
          go-version-file: go.mod
          cache: true
      - name: Release notes from CHANGELOG.md
        run: scripts/release_notes.sh "$GITHUB_REF_NAME" > "$RUNNER_TEMP/notes.md"
      - uses: goreleaser/goreleaser-action@v7.3.0
        with:
          version: v2.18.3
          args: release --clean --release-notes ${{ runner.temp }}/notes.md
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}
```

- [ ] **Step 3: Static check**

Run: `brew install actionlint && actionlint`
Expected: no output. (actionlint is a dev convenience, not a project dependency; do not add it to setup.md.)

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml
git commit -m "ci: test and lint on push; GoReleaser release on v* tags (D-032)"
```

---

### Task 4: Documentation and decisions

Independent of Tasks 1–3 in content; can run in parallel with them. `[P]`

**Files:**
- Create: `docs/kb/release.md`
- Modify: `docs/kb/README.md` (add a row), `docs/kb/conventions.md` ("Releases and versions": tagging goes through `make release-check NEXT=vX.Y.Z`; link `release.md`), `docs/PLAN.md` (M5 block: replace the four numbered items with one line pointing at `docs/plans/m5-release.md` and keep the D-024 note), `README.md` (new `## Install` section after the first code block), `CHANGELOG.md` (under `## [Unreleased]`, heading `### Added`)

**Interfaces:**
- Consumes: D-030…D-032 (logged in Step 0); target names from Tasks 1–3. GitHub access on this Mac is SSH (`origin` is `git@github.com:…`, `gh` protocol ssh, checked 2026-10-22), so the SSH tap URL is the right copy here.

- [ ] **Step 1: Write `docs/kb/release.md`**

Sections, each a short list: **Cut a release** (add the CHANGELOG entry under a new `## [X.Y.Z] - date` heading, `make release-check NEXT=vX.Y.Z`, `git tag -a`, `git push origin main vX.Y.Z`, watch the `Release` workflow, check the release page and `homebrew-rootout`); **Install from the tap** (the three commands from the spec plus `brew upgrade --cask rootout`); **Which binary am I running** (`which -a rootout`; tap for daily use, `make install` for a branch under test, never both on `PATH`); **Repair a failed release** (never move a tag: fix, add a CHANGELOG entry, cut a patch tag; delete a half-made GitHub Release by hand if GoReleaser left one); **Tokens** (`TAP_GITHUB_TOKEN`: fine-grained, `homebrew-rootout` only, Contents read/write, one-year expiry, rotate by creating a new one and updating the Actions secret; a user's `HOMEBREW_GITHUB_API_TOKEN`: read access to `shiva2021/rootout` releases); **Known limits** (unsigned, xattr hook, private tap needs git credentials, `GitHub.get_release` fallback).

- [ ] **Step 2: README Install section**

```bash
# Homebrew (private tap: needs git access to shiva2021/homebrew-rootout and a GitHub token)
export HOMEBREW_GITHUB_API_TOKEN=<token with read access to shiva2021/rootout>
brew tap shiva2021/rootout git@github.com:shiva2021/homebrew-rootout.git
brew install --cask rootout

# Direct download: grab rootout_<version>_darwin_<arch>.tar.gz from the Releases page, then
tar xzf rootout_*_darwin_*.tar.gz && xattr -d com.apple.quarantine rootout && mv rootout ~/.local/bin/

# From source
make install
```

One sentence below: the binary is not code-signed yet; Homebrew clears the quarantine flag for you.

- [ ] **Step 3: kb index, conventions, PLAN.md, CHANGELOG**

kb README row: `` `release.md` | Cutting, verifying or repairing a release; tap and token setup ``. CHANGELOG Unreleased: `- Release pipeline: GoReleaser builds darwin arm64/amd64 on every v* tag, GitHub Release notes come from this file, and a private Homebrew tap (shiva2021/homebrew-rootout) is updated (D-030…D-032). \`make release-check\` and \`make snapshot\`.`

- [ ] **Step 4: Commit**

```bash
git add docs README.md CHANGELOG.md
git commit -m "docs: release runbook, install section, M5 pointers"
```

---

### Task 5: CI goes live

Depends on Tasks 1–4 being committed on `main`.

- [ ] **Step 1: Push and watch**

Run `git push origin main`, then poll until the run for this commit appears (Monitor until-loop; foreground sleep is blocked):
```bash
gh run list --workflow CI --commit "$(git rev-parse HEAD)" --json databaseId,status --jq '.[0]'
```
and once it lists an id: `gh run watch --exit-status <id>`.
Expected: the `CI` run completes with exit 0. If `make test` fails on `macos-latest` but passes locally, the cause is environmental (no Homebrew casks, no Ollama, different `/usr/bin/trash`); fix the test to use its fake, never the workflow.

- [ ] **Step 2: Confirm the run time**

Run: `gh run list --workflow CI --limit 1`
Expected: under 5 minutes. Note the figure in the handover (it sets the billed-minutes estimate).

- [ ] **Step 3: Toolchain check still clean**

Run: `scripts/toolchain_check.sh`
Expected: no missing pin reported (the new files add no language).

---

### Task 6: First release through the pipeline (v0.4.0)

Manual steps are Shivang's; the agent prepares and verifies.

- [ ] **Step 1 (Shivang): tap repo and secret**

```bash
gh repo create shiva2021/homebrew-rootout --private --description "Homebrew tap for rootout" --add-readme
```
Then on github.com: Settings › Developer settings › Fine-grained tokens › Generate: resource owner shiva2021, repository access only `homebrew-rootout`, permission Contents: Read and write, expiry one year. Save it: `gh secret set TAP_GITHUB_TOKEN --repo shiva2021/rootout` (paste when prompted).

- [ ] **Step 2: CHANGELOG entry and gate**

Move the Unreleased items under `## [0.4.0] - <today>`; `make release-check NEXT=v0.4.0` passes. Commit: `release: v0.4.0 changelog`.

- [ ] **Step 3: Tag and push**

```bash
git tag -a v0.4.0 -m "v0.4.0: release pipeline (GoReleaser, CI, private Homebrew tap)"
git push origin main v0.4.0
```
Poll `gh run list --workflow Release --commit "$(git rev-parse v0.4.0^{commit})" --json databaseId --jq '.[0].databaseId'` until it prints an id (first ever Release run; the list is empty for a moment), then `gh run watch --exit-status <id>`.
Expected: exit 0.

- [ ] **Step 4: Inspect the release and the tap**

Run: `gh release view v0.4.0 --json assets,body --jq '.assets[].name, .body' && gh api repos/shiva2021/homebrew-rootout/contents/Casks/rootout.rb --jq .name`
Expected: `rootout_0.4.0_darwin_arm64.tar.gz`, `rootout_0.4.0_darwin_amd64.tar.gz`, `checksums.txt`; the body is the 0.4.0 CHANGELOG section; `rootout.rb`.

- [ ] **Step 5: Install from the tap on this Mac**

Shivang creates a second fine-grained token with Contents: Read on `shiva2021/rootout` (or reuses an existing one with that access) and exports it as `HOMEBREW_GITHUB_API_TOKEN`. Then:
```bash
brew tap shiva2021/rootout git@github.com:shiva2021/homebrew-rootout.git
brew install --cask rootout
which -a rootout; /opt/homebrew/bin/rootout --version; /opt/homebrew/bin/rootout doctor
```
Expected: `rootout version v0.4.0`; `doctor` runs normally with no Gatekeeper dialog. If the download fails inside the strategy class (the `GitHub.get_release` risk), replace `asset_id` with a `Utils::Curl` call to `https://api.github.com/repos/#{@owner}/#{@repo}/releases/tags/#{@tag}` parsed with `JSON.parse`, commit, and cut `v0.4.1`.

- [ ] **Step 6: Remove the shadow copy and re-check**

Run: `rm ~/.local/bin/rootout && hash -r && rootout --version`
Expected: `rootout version v0.4.0` from `/opt/homebrew/bin`. `brew uninstall --cask rootout && brew install --cask rootout` round-trips cleanly.

---

### Task 7: Handover and knowledge base refresh

- [ ] **Step 1: Update `docs/HANDOVER.md`**

New "Done this session (M5, D-030…D-032)" block: what shipped, the CI run time, whether the `TAP_GITHUB_TOKEN ?= snapshot` guard was kept, whether the strategy class needed the fallback, token expiry dates. Next steps: the remaining manual QA items (unchanged) and "M6 membership gate: write its plan". Blockers: add "unsigned binary relies on the xattr hook".

- [ ] **Step 2: Commit, tag if anything changed after v0.4.0, push**

```bash
git add docs/HANDOVER.md && git commit -m "docs: handover — M5 release pipeline live" && git push origin main
```
If Task 6 step 5 produced a code fix, this is the `v0.4.1` tag per `docs/kb/release.md`.

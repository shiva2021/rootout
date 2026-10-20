#!/usr/bin/env bash
# Detects the languages used in a project and reports whether each one has a
# version pin file its version manager reads. Run from the project root, or
# pass the root as the first argument. Exit code 1 if any pin is missing.
set -u
root="${1:-.}"
cd "$root" || { echo "cannot cd to $root"; exit 2; }

missing=0
found_any=0

check() {
  # $1 language, $2 marker (exists => language in use), $3 pin files (space separated), $4 hint
  local lang="$1" marker="$2" pins="$3" hint="$4"
  [ -e "$marker" ] || return 0
  found_any=1
  for p in $pins; do
    if [ -e "$p" ]; then
      printf '✔ %-12s pinned in %s (%s)\n' "$lang" "$p" "$(head -n1 "$p" 2>/dev/null | tr -d '\r' | cut -c1-40)"
      return 0
    fi
  done
  printf '✘ %-12s detected via %s but no pin file (%s). %s\n' "$lang" "$marker" "$pins" "$hint"
  missing=1
}

check "Node"    package.json        ".nvmrc .node-version"      "Add .nvmrc and use fnm."
check "Python"  pyproject.toml      ".python-version"           "Run: pyenv local <version>"
check "Python"  requirements.txt    ".python-version"           "Run: pyenv local <version>"
check "Rust"    Cargo.toml          "rust-toolchain.toml rust-toolchain" "Add rust-toolchain.toml."
check "Ruby"    Gemfile             ".ruby-version"             "Add .ruby-version (rbenv)."
check "Java"    build.gradle        ".sdkmanrc"                 "Add .sdkmanrc (SDKMAN)."
check "Java"    build.gradle.kts    ".sdkmanrc"                 "Add .sdkmanrc (SDKMAN)."
check "Java"    pom.xml             ".sdkmanrc"                 "Add .sdkmanrc (SDKMAN)."
check "PHP"     composer.json       ".php-version"              "Add .php-version."
check "Bun"     bun.lockb           ".bun-version"              "Add .bun-version."
check ".NET"    "$(ls *.sln *.csproj 2>/dev/null | head -n1)" "global.json" "Add global.json with sdk.version."

# Go pins inside go.mod itself.
if [ -e go.mod ]; then
  found_any=1
  if grep -qE '^go [0-9]' go.mod; then
    printf '✔ %-12s pinned in go.mod (%s)\n' "Go" "$(grep -E '^(go|toolchain) ' go.mod | tr '\n' ' ')"
  else
    printf '✘ %-12s go.mod has no go directive.\n' "Go"; missing=1
  fi
fi

# Lockfiles: not a pin, but worth flagging.
if [ -e package.json ] && ! ls package-lock.json pnpm-lock.yaml yarn.lock bun.lockb >/dev/null 2>&1; then
  echo "! Node         no lockfile committed (package-lock.json / pnpm-lock.yaml / yarn.lock)."
fi
if [ -e pyproject.toml ] && ! ls uv.lock poetry.lock >/dev/null 2>&1; then
  echo "! Python       no lockfile (uv.lock / poetry.lock)."
fi

# Installed managers on this machine.
echo "--- managers on this machine"
for m in fnm pyenv rustup rbenv sdk uv poetry; do
  if command -v "$m" >/dev/null 2>&1; then printf '  %s: yes\n' "$m"; else printf '  %s: no\n' "$m"; fi
done

[ "$found_any" -eq 1 ] || echo "No recognised project markers found in $(pwd)."
exit "$missing"

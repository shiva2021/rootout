#!/usr/bin/env bash
# Print the CHANGELOG.md section for one version, for GitHub Release notes.
# Usage: scripts/release_notes.sh vX.Y.Z [changelog]   (D-032)
# Exit 0 with the section body; 1 if the section is missing or empty; 2 on bad usage.
set -euo pipefail

if [[ $# -lt 1 || -z "$1" ]]; then
	echo "usage: release_notes.sh vX.Y.Z [changelog]" >&2
	exit 2
fi
ver="${1#v}"
file="${2:-CHANGELOG.md}"
if [[ ! -r "$file" ]]; then
	echo "release_notes: cannot read $file" >&2
	exit 2
fi

# found=1 once the heading "## [ver]" is seen; collect until the next "## " heading.
body="$(awk -v ver="$ver" '
	found && /^## / { exit }
	found { print }
	index($0, "## [" ver "]") == 1 { found = 1 }
	END { if (!found) exit 3 }
' "$file")" || {
	echo "release_notes: no CHANGELOG section for $ver" >&2
	exit 1
}

# Trim leading and trailing blank lines.
body="$(printf '%s\n' "$body" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')"
if [[ -z "${body//[[:space:]]/}" ]]; then
	echo "release_notes: empty CHANGELOG section for $ver" >&2
	exit 1
fi
printf '%s\n' "$body"

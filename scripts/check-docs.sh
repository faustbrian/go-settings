#!/usr/bin/env bash
set -euo pipefail

for package in $(go list ./...); do
    go doc "$package" >/dev/null
done

while IFS= read -r link; do
    target="${link#*(}"
    target="${target%)}"
    [[ "$target" == http* || "$target" == \#* ]] && continue
    target="${target%%#*}"
    [[ -e "$target" ]] || {
        printf 'broken README link: %s\n' "$target" >&2
        exit 1
    }
done < <(grep -oE '\[[^]]+\]\([^)]+\)' README.md)

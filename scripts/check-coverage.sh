#!/usr/bin/env bash
set -euo pipefail

packages="$(go list ./... | grep -v '/settingstest$' | paste -sd, -)"
go test -coverpkg="$packages" -coverprofile=coverage.out ./...
coverage="$(go tool cover -func=coverage.out | awk '/^total:/ {print $3}')"
if [[ "$coverage" != "100.0%" ]]; then
    printf 'production statement coverage is %s; required 100.0%%\n' "$coverage" >&2
    exit 1
fi

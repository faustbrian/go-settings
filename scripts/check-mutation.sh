#!/usr/bin/env bash
set -euo pipefail

go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash . \
    --integration --coverpkg ./... --workers 2 --test-cpu 2 \
    --timeout-coefficient 10 --threshold-mcover 100 \
    --threshold-efficacy 80 --output mutation-results.json \
    --exclude-files '^(audit|codec|import_export|key|operations|provider|registry|scope|snapshot)\.go$' \
    --exclude-files '^(audit|migration|postgres|settingstest|valkey)/'

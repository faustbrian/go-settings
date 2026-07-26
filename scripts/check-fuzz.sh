#!/usr/bin/env bash
set -euo pipefail

fuzz_time="${1:-2s}"
for target in \
    FuzzCodecsRejectMalformedPersistedDataWithoutPanicking \
    FuzzScopeIdentifiers \
    FuzzImportDocumentsFailClosed \
    FuzzResolutionOfMalformedStoredValues
do
    go test . -run='^$' -fuzz="$target" -fuzztime="$fuzz_time" \
        -parallel=4 -timeout=2m
done

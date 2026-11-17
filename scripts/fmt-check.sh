#!/usr/bin/env bash
# fmt-check.sh -- Fail if any Go source file in the repository is not gofmt-clean.
# Used by `make fmt-check` and by the Go CI workflows.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

echo "=== Go Formatting Check ==="

if ! command -v gofmt >/dev/null 2>&1; then
    echo "FAIL: gofmt is not available on PATH." >&2
    exit 1
fi

unformatted="$(gofmt -l .)"

if [ -n "$unformatted" ]; then
    echo "FAIL: the following Go files are not gofmt-clean:" >&2
    echo "$unformatted" >&2
    echo "Run 'make fmt' (or 'gofmt -w .') and commit the result." >&2
    exit 1
fi

echo "OK: all Go files are gofmt-clean."

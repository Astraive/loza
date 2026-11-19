#!/usr/bin/env bash
# bootstrap.sh -- One-command setup for a fresh clone (issue #59).
#
# Generated protobuf Go code under gen/go/loza/core/ is intentionally not
# committed. This script installs the pinned protobuf tools if needed,
# generates that code, and verifies the generated module builds. The ordinary
# `go build ./...` / `go test ./...` commands for collector and cortex then
# work as documented.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

GEN_DIR="$REPO_ROOT/gen/go/loza/core"
EXPECTED_GEN=(
    "event.pb.go" "ingest.pb.go" "collector.pb.go" "cortex.pb.go"
    "ingest_grpc.pb.go" "collector_grpc.pb.go" "cortex_grpc.pb.go"
)

echo "==> Generating protobuf Go code (installing pinned tools if needed)"
make proto

for gen in "${EXPECTED_GEN[@]}"; do
    if [ ! -f "$GEN_DIR/$gen" ]; then
        echo "error: missing generated file gen/go/loza/core/$gen" >&2
        exit 1
    fi
done

echo "==> Verifying the generated module builds"
(cd "$REPO_ROOT/gen/go" && go build ./...)

echo "Bootstrap complete. The collector and cortex can now be built with:"
echo "  (cd collector && go build ./...)"
echo "  (cd cortex && go build ./...)"

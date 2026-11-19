#!/usr/bin/env bash
# check-proto-sync.sh -- Verify that generated protobuf Go code matches proto source files.
# Exits with code 1 if any generated files are stale. Works both on an existing
# checkout and on a fresh clone where no generated files exist yet.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Proto Sync Check ==="
echo "Repo: $REPO_ROOT"
echo ""

# Step 1: Verify all proto source files exist
PROTO_SRC="$REPO_ROOT/proto/loza/core"
EXPECTED_PROTOS=("event.proto" "ingest.proto" "collector.proto" "cortex.proto")

for proto in "${EXPECTED_PROTOS[@]}"; do
    if [ ! -f "$PROTO_SRC/$proto" ]; then
        echo "✗ Missing proto source: $PROTO_SRC/$proto"
        exit 1
    fi
    echo "✓ Found: proto/loza/core/$proto"
done

# Step 2: Back up any generated files. On a fresh clone this is empty, which
# is expected: we regenerate first and only compare files that existed before.
GEN_DIR="$REPO_ROOT/gen/go/loza/core"
EXPECTED_GEN=(
    "event.pb.go" "ingest.pb.go" "collector.pb.go" "cortex.pb.go"
    "ingest_grpc.pb.go" "collector_grpc.pb.go" "cortex_grpc.pb.go"
)

BACKUP_DIR=$(mktemp -d)
trap 'rm -rf "$BACKUP_DIR"' EXIT
if [ -d "$GEN_DIR" ]; then
    cp "$GEN_DIR"/*.pb.go "$GEN_DIR"/*_grpc.pb.go "$BACKUP_DIR/" 2>/dev/null || true
fi

HAD_GENERATED=1
if [ -n "$(ls -A "$BACKUP_DIR")" ]; then
    HAD_GENERATED=0
fi

# Step 3: Regenerate (make proto installs missing tools itself).
echo ""
echo "Regenerating proto code and comparing..."
(cd "$REPO_ROOT" && make proto) || {
    echo "✗ Failed to regenerate proto files"
    exit 1
}

# Step 4: Every expected file must exist after regeneration.
FAILED=0
for gen in "${EXPECTED_GEN[@]}"; do
    if [ ! -f "$GEN_DIR/$gen" ]; then
        echo "✗ Missing generated file after regeneration: gen/go/loza/core/$gen"
        FAILED=1
    fi
done

if [ "$FAILED" -eq 1 ]; then
    echo ""
    echo "✗ Proto sync check FAILED — generated code is incomplete."
    exit 1
fi

# Step 5: On a fresh clone there is nothing to diff against; regeneration
# succeeding is sufficient. Otherwise compare old and new byte-for-byte.
if [ "$HAD_GENERATED" -eq 1 ]; then
    echo "✓ No generated files were present before regeneration (clean clone)"
else
    for f in "$BACKUP_DIR"/*.pb.go; do
        basename=$(basename "$f")
        if ! cmp -s "$f" "$GEN_DIR/$basename"; then
            echo "✗ Out of date: gen/go/loza/core/$basename (regeneration produces different output)"
            FAILED=1
        fi
    done
    if [ "$FAILED" -eq 1 ]; then
        echo ""
        echo "✗ Proto sync check FAILED — run 'make proto' and commit the updated files."
        exit 1
    fi
    echo "✓ All generated files match after regeneration"
fi

echo ""
echo "✓ Proto sync check passed"

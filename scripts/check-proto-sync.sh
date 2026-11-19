#!/usr/bin/env bash
# check-proto-sync.sh -- Verify that generated protobuf Go code matches proto source files.
# Exits with code 1 if any generated files are stale. Works both on an existing
# checkout and on a fresh clone where no generated files exist yet.
# Exits with code 1 if any generated files are stale or missing.
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
# Step 2: Check that generated Go files are present. A fresh clone should have
# these committed, but the check still behaves sensibly if they were deleted.
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
missing=0
for gen in "${EXPECTED_GEN[@]}"; do
    if [ ! -f "$GEN_DIR/$gen" ]; then
        echo "✗ Missing generated file: gen/go/loza/core/$gen (run 'make proto' and commit it)"
        missing=1
    else
        echo "✓ Found: gen/go/loza/core/$gen"
    fi
done

# Step 3: Regenerate and compare. The protoc version comment is ignored so the
# check works with any protoc release, and unmatched globs are never treated as
# real files.
echo ""
echo "Regenerating proto code and comparing..."

BACKUP_DIR=$(mktemp -d)
trap 'rm -rf "$BACKUP_DIR"' EXIT

if [ "$missing" -eq 0 ]; then
    cp "$GEN_DIR"/*.pb.go "$BACKUP_DIR/"
fi

if ! (cd "$REPO_ROOT" && make proto); then
    echo "✗ Failed to regenerate proto files"
    exit 1
fi

if [ "$missing" -eq 1 ]; then
    echo ""
    echo "✗ Generated files were missing; 'make proto' has recreated them."
    echo "  Commit gen/go/loza/core/*.pb.go so fresh clones build without codegen."
    exit 1
fi

# Step 4: Every expected file must exist after regeneration.
FAILED=0
for gen in "${EXPECTED_GEN[@]}"; do
    if [ ! -f "$GEN_DIR/$gen" ]; then
        echo "✗ Missing generated file after regeneration: gen/go/loza/core/$gen"
FAILED=0
for f in "$BACKUP_DIR"/*.pb.go; do
    [ -f "$f" ] || continue
    basename=$(basename "$f")
    if [ ! -f "$GEN_DIR/$basename" ]; then
        echo "✗ Missing generated file after regeneration: gen/go/loza/core/$basename"
        FAILED=1
        continue
    fi
    # Ignore the protoc version line; it varies with the installed protoc.
    sed '/^\/\/.*[[:space:]]protoc[[:space:]].*v[0-9]/d' "$f" > "$BACKUP_DIR/old.norm"
    sed '/^\/\/.*[[:space:]]protoc[[:space:]].*v[0-9]/d' "$GEN_DIR/$basename" > "$BACKUP_DIR/new.norm"
    if ! cmp -s "$BACKUP_DIR/old.norm" "$BACKUP_DIR/new.norm"; then
        echo "✗ Out of date: gen/go/loza/core/$basename (regeneration produces different output)"
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

    echo "✗ Proto sync check FAILED — run 'make bootstrap' (or 'make proto') and commit the updated files."
    exit 1
fi

echo "✓ All generated files match after regeneration"
echo ""
echo "✓ Proto sync check passed"

#!/usr/bin/env bash
# check-proto-sync.sh -- Verify generated protobuf Go code matches proto sources.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PROTO_SRC="$REPO_ROOT/proto/loza/core"
GEN_DIR="$REPO_ROOT/gen/go/loza/core"
EXPECTED_PROTOS=(event.proto ingest.proto collector.proto cortex.proto)
EXPECTED_GEN=(event.pb.go ingest.pb.go collector.pb.go cortex.pb.go ingest_grpc.pb.go collector_grpc.pb.go cortex_grpc.pb.go)

for proto in "${EXPECTED_PROTOS[@]}"; do
    if [ ! -f "$PROTO_SRC/$proto" ]; then
        echo "✗ Missing proto source: $PROTO_SRC/$proto"
        exit 1
    fi
done

for gen in "${EXPECTED_GEN[@]}"; do
    if [ ! -f "$GEN_DIR/$gen" ]; then
        echo "✗ Missing committed generated file: $GEN_DIR/$gen"
        exit 1
    fi
done

BACKUP_DIR=$(mktemp -d)
BACKUP_SUCCEEDED=()
restore_generated_files() {
    local i gen status=0
    for i in "${!EXPECTED_GEN[@]}"; do
        gen="${EXPECTED_GEN[$i]}"
        if [ "${BACKUP_SUCCEEDED[$i]-}" = yes ]; then
            cp -p "$BACKUP_DIR/$gen" "$GEN_DIR/$gen" || status=1
        fi
    done
    rm -rf "$BACKUP_DIR" || status=1
    return "$status"
}
trap restore_generated_files EXIT

for i in "${!EXPECTED_GEN[@]}"; do
    gen="${EXPECTED_GEN[$i]}"
    if cp -p "$GEN_DIR/$gen" "$BACKUP_DIR/$gen"; then
        BACKUP_SUCCEEDED[$i]=yes
    else
        echo "✗ Failed to back up generated file: $GEN_DIR/$gen"
        exit 1
    fi
done

if ! (cd "$REPO_ROOT" && make proto); then
    echo "✗ Failed to regenerate proto files"
    exit 1
fi

FAILED=0
for gen in "${EXPECTED_GEN[@]}"; do
    if [ ! -f "$GEN_DIR/$gen" ]; then
        echo "✗ Missing generated file after regeneration: gen/go/loza/core/$gen"
        FAILED=1
        continue
    fi
    sed '/^\/\/.*[[:space:]]protoc[[:space:]].*v[0-9]/d' "$BACKUP_DIR/$gen" > "$BACKUP_DIR/old.norm"
    sed '/^\/\/.*[[:space:]]protoc[[:space:]].*v[0-9]/d' "$GEN_DIR/$gen" > "$BACKUP_DIR/new.norm"
    if ! cmp -s "$BACKUP_DIR/old.norm" "$BACKUP_DIR/new.norm"; then
        echo "✗ Out of date: gen/go/loza/core/$gen (regeneration produces different output)"
        FAILED=1
    fi
done

if [ "$FAILED" -eq 1 ]; then
    echo "✗ Proto sync check FAILED — run 'make proto' and commit the updated files."
    exit 1
fi
echo "✓ All generated files match after regeneration"
echo "✓ Proto sync check passed"

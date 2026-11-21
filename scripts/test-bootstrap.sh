#!/usr/bin/env bash
# test-bootstrap.sh -- Regression test for MASS-0997 (issue #59).
#
# Verifies missing committed outputs fail the sync check, bootstrap regenerates
# them, and collector/cortex resolve the generated package.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

GEN_DIR="$REPO_ROOT/gen/go/loza/core"
EXPECTED_GEN=(
    "event.pb.go" "ingest.pb.go" "collector.pb.go" "cortex.pb.go"
    "ingest_grpc.pb.go" "collector_grpc.pb.go" "cortex_grpc.pb.go"
)

BACKUP_DIR="$(mktemp -d)"
declare -A GENERATED_EXISTED=()
declare -A BACKUP_SUCCEEDED=()

restore_generated() {
    local gen status=0
    for gen in "${EXPECTED_GEN[@]}"; do
        if [ "${BACKUP_SUCCEEDED[$gen]-}" = yes ]; then
            cp -p "$BACKUP_DIR/$gen" "$GEN_DIR/$gen" || status=1
        elif [ "${GENERATED_EXISTED[$gen]-}" = no ]; then
            rm -f "$GEN_DIR/$gen" || status=1
        fi
    done
    rm -rf "$BACKUP_DIR" || status=1
    return "$status"
}
trap restore_generated EXIT

for gen in "${EXPECTED_GEN[@]}"; do
    if [ -e "$GEN_DIR/$gen" ]; then
        GENERATED_EXISTED["$gen"]=yes
        if cp -p "$GEN_DIR/$gen" "$BACKUP_DIR/$gen"; then
            BACKUP_SUCCEEDED["$gen"]=yes
        else
            fail "failed to back up generated file: $gen"
        fi
    else
        GENERATED_EXISTED["$gen"]=no
    fi
done

clean_generated() {
    local gen
    for gen in "${EXPECTED_GEN[@]}"; do
        rm -f "$GEN_DIR/$gen"
    done
}

# 1. The bootstrap entry point and self-bootstrapping proto target must exist.
grep -Eq '^bootstrap:' Makefile || fail "Makefile has no 'bootstrap' target"
grep -Eq '^proto:[[:space:]]*proto-tools' Makefile || fail "'make proto' does not install its own tools"

# 2. Generated protobuf Go files are intentionally committed and checked for
# drift; bootstrap must still work without removing those tracked artifacts.
for gen in "${EXPECTED_GEN[@]}"; do
    git ls-files --error-unmatch "$GEN_DIR/$gen" >/dev/null || fail "generated protobuf file is not tracked: $gen"
done

# 3. The one-command bootstrap must be documented for new contributors.
grep -q 'make bootstrap' README.md || fail "README.md does not document 'make bootstrap'"
grep -q 'make bootstrap' docs/contributing.md || fail "docs/contributing.md does not document 'make bootstrap'"
grep -q 'make bootstrap' docs/getting-started.md || fail "docs/getting-started.md does not document 'make bootstrap'"

# 4. The sync check must reject missing committed outputs.
clean_generated
if bash scripts/check-proto-sync.sh >/dev/null 2>&1; then
    fail "check-proto-sync.sh succeeds when committed generated files are missing"
fi

# 5. End to end: the documented bootstrap makes a clean clone buildable.
clean_generated
make bootstrap >/dev/null || fail "make bootstrap failed from a clean clone"

for gen in "${EXPECTED_GEN[@]}"; do
    [ -f "$GEN_DIR/$gen" ] || fail "bootstrap did not generate gen/go/loza/core/$gen"
done

(cd "$REPO_ROOT/gen/go" && go build ./...) || fail "gen/go does not build after bootstrap"

# The clean-clone symptom is the unresolved generated import. Assert that it
# is gone from both services (other, unrelated source errors are out of scope).
for component in collector cortex; do
    build_output=$(cd "$REPO_ROOT/$component" && go build ./... 2>&1) || true
    if echo "$build_output" | grep -q 'no required module provides package github.com/astraive/loza/gen/go/loza/core'; then
        fail "$component still cannot resolve the generated protobuf package"
    fi
    echo "✓ $component resolves github.com/astraive/loza/gen/go/loza/core"
done

# 6. A missing committed generated file must fail the sync check. Bootstrap
# regenerates the outputs, after which sync and the generated-package build pass.
rm -f "$GEN_DIR/${EXPECTED_GEN[0]}"
if bash scripts/check-proto-sync.sh >/dev/null 2>&1; then
    fail "check-proto-sync.sh succeeds when a committed generated file is missing"
fi
make bootstrap >/dev/null || fail "make bootstrap failed after removing a generated file"
bash scripts/check-proto-sync.sh >/dev/null || fail "check-proto-sync.sh fails after bootstrap"
(cd "$REPO_ROOT/gen/go" && go build ./...) || fail "gen/go does not build after bootstrap"

echo "PASS: 'make bootstrap' generates protobuf code that resolves from collector and cortex"

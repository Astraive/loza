#!/usr/bin/env bash
# test-bootstrap.sh -- Regression test for MASS-0997 (issue #59).
#
# Fails if a fresh clone cannot generate its protobuf Go code with the single
# documented bootstrap command, or if collector/cortex still cannot resolve
# the generated package. Passes once `make bootstrap` is self-contained and the
# sync check tolerates a clean tree.
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

clean_generated() {
    rm -f "$GEN_DIR"/*.pb.go "$GEN_DIR"/*_grpc.pb.go
}

# 1. The bootstrap entry point and self-bootstrapping proto target must exist.
grep -Eq '^bootstrap:' Makefile || fail "Makefile has no 'bootstrap' target"
grep -Eq '^proto:[[:space:]]*proto-tools' Makefile || fail "'make proto' does not install its own tools"

# 2. Policy check: generated .pb.go files are produced on demand, not committed.
if git ls-files 'gen/go/loza/core/*.pb.go' | grep -q .; then
    fail "generated .pb.go files are committed (this fix relies on on-demand generation)"
fi

# 3. The one-command bootstrap must be documented for new contributors.
grep -q 'make bootstrap' README.md || fail "README.md does not document 'make bootstrap'"
grep -q 'make bootstrap' docs/contributing.md || fail "docs/contributing.md does not document 'make bootstrap'"
grep -q 'make bootstrap' docs/getting-started.md || fail "docs/getting-started.md does not document 'make bootstrap'"

# 4. Proto sync check must succeed with no generated files present (clean clone).
clean_generated
bash scripts/check-proto-sync.sh >/dev/null || fail "check-proto-sync.sh fails on a clean clone"

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

echo "PASS: 'make bootstrap' generates protobuf code that resolves from collector and cortex"

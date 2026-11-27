#!/usr/bin/env bash
# run-collector.sh -- Run collector benchmarks (DuckDB + projection + load test).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RESULTS_DIR="$SCRIPT_DIR/results"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$RESULTS_DIR"

source "$SCRIPT_DIR/lib/json-reporter.sh"
BENCH_FAILED=0

if ! command -v go &>/dev/null; then
    echo "ERROR: go is required for collector benchmarks"
    exit 1
fi

cd "$REPO_ROOT/collector"

echo "=== Collector Benchmarks ==="

# --- DuckDB Sink ---
echo "--- DuckDB Sink ---"
DUCKDB_OUT="$RESULTS_DIR/collector-duckdb-${TIMESTAMP}.json"
if go test ./internal/sinks/duckdb -run='^$' -bench=. -benchmem -count=1 -json 2>/dev/null | \
    python3 "$SCRIPT_DIR/lib/parse-go-json.py" collector-duckdb collector "$TIMESTAMP" \
    > "$DUCKDB_OUT"; then
    echo "  PASS: collector-duckdb"
else
    echo "  FAIL: collector-duckdb"
    BENCH_FAILED=1
fi

# --- Projection ---
echo "--- Projection ---"
PROJ_OUT="$RESULTS_DIR/collector-projection-${TIMESTAMP}.json"
if go test ./internal/sinks/internal/projection -run='^$' -bench=. -benchmem -count=1 -json 2>/dev/null | \
    python3 "$SCRIPT_DIR/lib/parse-go-json.py" collector-projection collector "$TIMESTAMP" \
    > "$PROJ_OUT"; then
    echo "  PASS: collector-projection"
else
    echo "  FAIL: collector-projection"
    BENCH_FAILED=1
fi

# --- Load Test ---
echo "--- Load Test ---"
LOAD_OUT="$RESULTS_DIR/collector-load-${TIMESTAMP}.json"
if go test ./internal/sinks/duckdb -run='^$' -bench=BenchmarkLoad -benchtime=5s -benchmem -count=1 -json 2>/dev/null | \
    python3 "$SCRIPT_DIR/lib/parse-go-json.py" collector-load collector "$TIMESTAMP" --allow-empty \
    > "$LOAD_OUT"; then
    if python3 - "$LOAD_OUT" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as report:
    sys.exit(0 if json.load(report).get("results") else 1)
PY
    then
        echo "  PASS: collector-load"
    else
        echo "  SKIP: collector-load (no load benchmark found)"
    fi
else
    echo "  FAIL: collector-load"
    BENCH_FAILED=1
fi

cd "$REPO_ROOT"
echo ""
echo "Results saved to: $RESULTS_DIR/"
exit "$BENCH_FAILED"

#!/usr/bin/env bash
# run-collector.sh -- Run collector benchmarks (DuckDB + projection + load test).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RESULTS_DIR="$SCRIPT_DIR/results"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$RESULTS_DIR"

source "$SCRIPT_DIR/lib/json-reporter.sh"

if ! command -v go &>/dev/null; then
    echo "ERROR: go is required for collector benchmarks"
    exit 1
fi

cd "$REPO_ROOT/collector"

echo "=== Collector Benchmarks ==="

# --- DuckDB Sink ---
echo "--- DuckDB Sink ---"
DUCKDB_OUT="$RESULTS_DIR/collector-duckdb-${TIMESTAMP}.json"
go test ./internal/sinks/duckdb -run='^$' -bench=. -benchmem -count=1 -json 2>/dev/null | \
    python3 "$SCRIPT_DIR/lib/parse-go-json.py" collector-duckdb collector "$TIMESTAMP" \
    > "$DUCKDB_OUT" && echo "  PASS: collector-duckdb" || echo "  FAIL: collector-duckdb"

# --- Projection ---
echo "--- Projection ---"
PROJ_OUT="$RESULTS_DIR/collector-projection-${TIMESTAMP}.json"
go test ./internal/sinks/internal/projection -run='^$' -bench=. -benchmem -count=1 -json 2>/dev/null | \
    python3 "$SCRIPT_DIR/lib/parse-go-json.py" collector-projection collector "$TIMESTAMP" \
    > "$PROJ_OUT" && echo "  PASS: collector-projection" || echo "  FAIL: collector-projection"

# --- Load Test ---
echo "--- Load Test ---"
LOAD_OUT="$RESULTS_DIR/collector-load-${TIMESTAMP}.json"
go test ./internal/sinks/duckdb -run='^$' -bench=BenchmarkLoad -benchtime=5s -benchmem -count=1 -json 2>/dev/null | \
    python3 "$SCRIPT_DIR/lib/parse-go-json.py" collector-load collector "$TIMESTAMP" \
    > "$LOAD_OUT" && echo "  PASS: collector-load" || echo "  FAIL: collector-load"

cd "$REPO_ROOT"
echo ""
echo "Results saved to: $RESULTS_DIR/"

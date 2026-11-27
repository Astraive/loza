#!/usr/bin/env python3
"""Parse go test -json benchmark output into the benchmark result format."""
import json
import sys


def parse_benchmark(output):
    parts = output.strip().split()
    if len(parts) < 4 or not parts[0].startswith("Benchmark"):
        raise ValueError(f"unexpected benchmark output: {output.rstrip()!r}")
    if parts[3] != "ns/op":
        raise ValueError(f"expected ns/op at token 4: {output.rstrip()!r}")
    entry = {
        "name": parts[0],
        "iterations": int(parts[1]),
        "ns_per_op": float(parts[2]),
        "pass": True,
    }
    for index, unit, key, converter in (
        (4, "B/op", "bytes_per_op", int),
        (6, "allocs/op", "allocs_per_op", int),
    ):
        if len(parts) > index:
            if len(parts) <= index + 1 or parts[index + 1] != unit:
                raise ValueError(f"expected {unit} after token {index + 1}: {output.rstrip()!r}")
            entry[key] = converter(parts[index])
    return entry


def main():
    if len(sys.argv) not in (4, 5) or (len(sys.argv) == 5 and sys.argv[4] != "--allow-empty"):
        raise ValueError("usage: parse-go-json.py SUITE COMPONENT TIMESTAMP [--allow-empty]")
    suite, component, timestamp = sys.argv[1:4]
    allow_empty = len(sys.argv) == 5
    results = []
    for line in sys.stdin:
        try:
            obj = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid go test JSON: {line.rstrip()!r}") from exc
        output = obj.get("Output", "")
        if obj.get("Action") == "output" and output.lstrip().startswith("Benchmark"):
            results.append(parse_benchmark(output))
    if not results and not allow_empty:
        raise ValueError("go test produced no benchmark result rows")
    json.dump({
        "suite": suite,
        "component": component,
        "timestamp": timestamp,
        "results": results,
        "summary": {"total": len(results), "passed": len(results), "failed": 0},
    }, sys.stdout, indent=2)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, IndexError) as exc:
        print(f"Go benchmark parse error: {exc}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
"""Parse vitest bench output into JSON."""
import json, re, sys

# Markers used by vitest's default reporter (reporters/renderers/utils.ts):
#   ·  a passing benchmark row
#   ×  a failing benchmark/test row
# A "FAIL  <file> > <name>" banner is also printed for failed tasks/files.
BENCH_MARKER = '\u00b7'
FAIL_MARKER = '\u00d7'


def decode_line(raw):
    """Decode a line of vitest output as UTF-8, tolerating legacy latin-1 logs."""
    try:
        return raw.decode('utf-8')
    except UnicodeDecodeError:
        return raw.decode('latin-1')


def leaf_name(text):
    """Reduce a vitest task path ('suite > bench') to its leaf benchmark name."""
    return re.split(r'\s+>\s+', text.strip())[-1].strip()


results = []
seen = set()


def add_result(name, ns_per_op, hz, passed):
    """Append a result, ignoring duplicate rows emitted by the reporter."""
    if not name:
        return
    key = (name, ns_per_op, hz, passed)
    if key in seen:
        return
    seen.add(key)
    results.append({
        'name': name,
        'ns_per_op': ns_per_op,
        'hz': hz,
        'pass': passed,
    })


with open(sys.argv[1], 'rb') as f:
    for raw_line in f:
        stripped = decode_line(raw_line).strip()
        if not stripped:
            continue

        if stripped.startswith(BENCH_MARKER):
            parts = re.split(r'\s{2,}', stripped[1:].strip())
            if len(parts) >= 5:
                try:
                    hz = float(parts[1].replace(',', ''))
                    mean_ns = float(parts[4]) * 1_000_000
                except ValueError:
                    continue
                add_result(parts[0].strip(), round(mean_ns, 2), round(hz, 2), True)
        elif stripped.startswith(FAIL_MARKER):
            add_result(leaf_name(stripped[1:]), 0, 0, False)
        elif re.match(r'^FAIL\b', stripped):
            add_result(leaf_name(re.sub(r'^FAIL\s+', '', stripped)), 0, 0, False)

suite = sys.argv[2] if len(sys.argv) > 2 else 'sdk-js'
timestamp = sys.argv[3] if len(sys.argv) > 3 else ''
out_file = sys.argv[4] if len(sys.argv) > 4 else None

passed = sum(1 for r in results if r['pass'])
failed = len(results) - passed

output = {
    'suite': suite,
    'component': 'sdks/js/bench',
    'timestamp': timestamp,
    'results': results,
    'summary': {'total': len(results), 'passed': passed, 'failed': failed}
}

if out_file:
    with open(out_file, 'w') as f:
        json.dump(output, f, indent=2)
else:
    print(json.dumps(output, indent=2))

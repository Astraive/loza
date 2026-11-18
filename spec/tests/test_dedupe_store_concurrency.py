"""Regression tests for cross-process atomicity of the local dedupe store.

The fallback store used by ``build_store`` is shared by every process that has no
Redis configured. ``mark_seen`` must therefore be atomic across processes: only one
caller may observe the key as new for a given idempotency window.
"""
import multiprocessing
import tempfile
import unittest
from pathlib import Path
import sys

repo_root = Path(__file__).resolve().parents[2]
spec_dir = repo_root / 'spec'
sys.path.insert(0, str(spec_dir))

from services.dedupe_store import LocalDedupeStore


def _mark_seen_worker(store_path, key, ttl, start_barrier, results):
    """Each worker builds its own store instance against the shared path."""
    store = LocalDedupeStore(store_path)
    start_barrier.wait()
    results.put(store.mark_seen(key, ttl))


class TestLocalDedupeStoreConcurrency(unittest.TestCase):
    def test_concurrent_mark_seen_admits_exactly_one_caller(self):
        ctx = multiprocessing.get_context('fork')
        workers = 16
        with tempfile.TemporaryDirectory() as tmp:
            store_path = str(Path(tmp) / 'dedupe_store')
            key = 'evt-concurrent-1'
            ttl = 300

            barrier = ctx.Barrier(workers)
            results = ctx.Queue()
            procs = [
                ctx.Process(
                    target=_mark_seen_worker,
                    args=(store_path, key, ttl, barrier, results),
                )
                for _ in range(workers)
            ]
            for proc in procs:
                proc.start()
            observed = [results.get(timeout=30) for _ in range(workers)]
            for proc in procs:
                proc.join(timeout=30)
                self.assertEqual(proc.exitcode, 0)

        admitted = sum(1 for was_new in observed if was_new)
        self.assertEqual(admitted, 1,
                         'concurrent mark_seen admitted %d callers, expected 1'
                         % admitted)


if __name__ == '__main__':
    unittest.main()

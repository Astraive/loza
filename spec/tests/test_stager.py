import json
import os
import threading
import unittest
from pathlib import Path
import sys
from unittest.mock import patch

repo_root = Path(__file__).resolve().parents[2]
spec_dir = repo_root / 'spec'
sys.path.insert(0, str(spec_dir))

from services import stager
from services.queue_prototype import LocalQueue
from services.stager import build_queue, stage_events

class TestStager(unittest.TestCase):
    def setUp(self):
        self.spec_dir = spec_dir
        self.queue = LocalQueue(str(self.spec_dir / 'tmp_stager_queue'))
        for f in list(self.queue.path.glob('*.json')):
            try: f.unlink()
            except: pass
        self.staging = self.spec_dir / 'tmp_staging'
        self.atomic_staging = self.spec_dir / 'tmp_staging_atomic'
        for base in (self.staging, self.atomic_staging):
            if base.exists():
                for p in base.rglob('*'):
                    try:
                        if p.is_file(): p.unlink()
                    except: pass

    def test_produce_keeps_events_with_same_clock_value(self):
        event = {
            "event_id": "stg-same-clock",
            "timestamp": "2026-05-18T12:30:45Z",
            "service": "s",
            "schema_version": "v1",
            "event_version": "v1",
            "event": "same-clock",
        }
        with patch("services.queue_prototype.time.time", return_value=1_716_035_845.0):
            first = Path(self.queue.produce(event))
            second = Path(self.queue.produce(event))

        self.assertNotEqual(first, second)
        self.assertTrue(first.exists())
        self.assertTrue(second.exists())

    def test_stage_two_events(self):
        e1 = {'event_id': 'stg-1', 'timestamp': '2026-05-18T12:30:45Z', 'service': 's', 'schema_version': 'v1', 'event_version': 'v1', 'event': 'a'}
        e2 = {'event_id': 'stg-2', 'timestamp': '2026-05-18T12:30:46Z', 'service': 's', 'schema_version': 'v1', 'event_version': 'v1', 'event': 'b'}
        self.queue.produce(e1)
        self.queue.produce(e2)
        n = stage_events(self.queue, self.staging, batch_size=10, compress=True)
        self.assertEqual(n, 2)
        # check files
        parts = list(self.staging.glob('**/stg-1.json.gz'))
        self.assertTrue(len(parts) == 1)
        parts2 = list(self.staging.glob('**/stg-2.json.gz'))
        self.assertTrue(len(parts2) == 1)

    def test_existing_target_is_not_overwritten(self):
        # Simulate a competing worker that already committed the preferred name.
        staging = self.atomic_staging
        payload = {
            "event_id": "race-evt",
            "timestamp": "2026-05-18T12:30:45Z",
            "service": "s",
            "schema_version": "v1",
            "event_version": "v1",
            "event": "ours",
        }
        partition_dir = stager._partition_path(staging, payload["timestamp"])
        partition_dir.mkdir(parents=True, exist_ok=True)
        first = partition_dir / "race-evt.json"
        first.write_text("competitor", encoding="utf-8")

        target = stager._stage_payload(staging, payload, "src", False)

        self.assertEqual(target, partition_dir / "race-evt-1.json")
        self.assertEqual(first.read_text(encoding="utf-8"), "competitor")
        self.assertEqual(
            target.read_text(encoding="utf-8"),
            json.dumps(payload, separators=(",", ":")),
        )

    def test_concurrent_staging_preserves_both_payloads(self):
        # Force two workers to reach the final-name claim at the same instant,
        # reproducing the TOCTOU race that used to let one overwrite the other.
        staging = self.atomic_staging
        payload_a = {
            "event_id": "race-concurrent",
            "timestamp": "2026-05-18T12:30:45Z",
            "event": "A",
        }
        payload_b = dict(payload_a, event="B")
        barrier = threading.Barrier(2)
        real_link = os.link

        def contested_link(src, dst):
            if Path(dst).name == "race-concurrent.json":
                barrier.wait(timeout=10)
            return real_link(src, dst)

        results, errors = [], []

        def worker(payload):
            try:
                results.append(stager._stage_payload(staging, payload, "src", False))
            except Exception as exc:  # pragma: no cover - surfaces in assertions
                errors.append(exc)

        with patch("services.stager.os.link", side_effect=contested_link):
            threads = [threading.Thread(target=worker, args=(p,)) for p in (payload_a, payload_b)]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join(timeout=15)

        self.assertEqual(errors, [])
        self.assertEqual(len(results), 2)
        self.assertEqual(len(set(results)), 2)
        expected = {
            json.dumps(p, separators=(",", ":")) for p in (payload_a, payload_b)
        }
        self.assertEqual({p.read_text(encoding="utf-8") for p in results}, expected)
        staged_files = [p for p in staging.rglob("*") if p.is_file()]
        self.assertEqual(len(staged_files), 2)
        self.assertFalse([p for p in staging.rglob("*.tmp")])

    def test_build_queue_local_backend(self):
        queue = build_queue(
            "local",
            queue_dir=str(self.spec_dir / "tmp_stager_queue"),
            kafka_brokers="",
            kafka_topic="",
            kafka_group_id="",
            kafka_dlq_topic="",
            kafka_auto_offset_reset="earliest",
            kafka_poll_timeout_seconds=1.0,
            kafka_flush_timeout_seconds=10.0,
            retry_policy=None,
        )
        self.assertIsInstance(queue, LocalQueue)

    def test_build_queue_kafka_requires_configuration(self):
        with self.assertRaises(ValueError):
            build_queue(
                "kafka",
                queue_dir=str(self.spec_dir / "tmp_stager_queue"),
                kafka_brokers="",
                kafka_topic="",
                kafka_group_id="",
                kafka_dlq_topic="",
                kafka_auto_offset_reset="earliest",
                kafka_poll_timeout_seconds=1.0,
                kafka_flush_timeout_seconds=10.0,
                retry_policy=None,
            )

    def test_build_queue_kafka_backend(self):
        sentinel = object()
        with patch("services.stager.KafkaAdapter", return_value=sentinel) as adapter_cls:
            queue = build_queue(
                "kafka",
                queue_dir=str(self.spec_dir / "tmp_stager_queue"),
                kafka_brokers="kafka:9092",
                kafka_topic="events",
                kafka_group_id="stager",
                kafka_dlq_topic="events.dlq",
                kafka_auto_offset_reset="latest",
                kafka_poll_timeout_seconds=0.5,
                kafka_flush_timeout_seconds=12.0,
                retry_policy=None,
            )
        self.assertIs(queue, sentinel)
        adapter_cls.assert_called_once()

    def tearDown(self):
        for f in list(self.queue.path.glob('*.json')):
            try: f.unlink()
            except: pass
        for base in (self.staging, self.atomic_staging):
            if base.exists():
                for p in base.rglob('*'):
                    try:
                        if p.is_file(): p.unlink()
                    except: pass

if __name__ == '__main__':
    unittest.main()

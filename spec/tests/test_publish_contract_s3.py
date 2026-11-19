"""Regression tests for the CloudFront invalidation CallerReference.

CloudFront requires each invalidation's CallerReference to be unique. The
publish script previously derived it from whole seconds (``int(time.time())``),
so two publishes in the same second reused the reference and the second
invalidation failed with ``InvalidationBatchAlreadyExists``.
"""
import importlib.util
import os
import sys
import types
import unittest
import uuid
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPO_ROOT / "spec" / "scripts" / "publish_contract_s3.py"


def _load_module():
    spec = importlib.util.spec_from_file_location("publish_contract_s3", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeS3:
    def __init__(self):
        self.uploaded = []

    def upload_file(self, filename, bucket, key, ExtraArgs=None):
        self.uploaded.append((filename, bucket, key))


class FakeCloudFront:
    def __init__(self):
        self.invalidations = []

    def create_invalidation(self, DistributionId, InvalidationBatch):
        self.invalidations.append((DistributionId, InvalidationBatch))


class FakeBoto3(types.ModuleType):
    def __init__(self):
        super().__init__("boto3")
        self.s3 = FakeS3()
        self.cloudfront = FakeCloudFront()

    def client(self, name):
        return {"s3": self.s3, "cloudfront": self.cloudfront}[name]


class TestCloudFrontCallerReference(unittest.TestCase):
    def test_consecutive_invalidations_use_unique_caller_reference(self):
        """Two publishes in the same second must not reuse a CallerReference."""
        module = _load_module()
        fake_boto3 = FakeBoto3()
        env = {
            "LOZA_CONTRACT_BUCKET": "test-bucket",
            "CLOUDFRONT_DISTRIBUTION_ID": "TESTDIST",
        }

        # Freeze wall-clock time: the old whole-second reference would collide
        # deterministically here, while a UUID-based reference stays unique.
        with mock.patch.dict(os.environ, env, clear=True), \
                mock.patch.dict(sys.modules, {"boto3": fake_boto3}), \
                mock.patch.object(sys, "argv", ["publish_contract_s3.py"]), \
                mock.patch.object(module, "_load_local_registry", return_value=SCRIPT_PATH.parent), \
                mock.patch("time.time", return_value=1700000000.0):
            self.assertEqual(module.main(), 0)
            self.assertEqual(module.main(), 0)

        invalidations = fake_boto3.cloudfront.invalidations
        self.assertEqual(len(invalidations), 2)
        refs = [batch["CallerReference"] for _, batch in invalidations]
        self.assertNotEqual(
            refs[0], refs[1],
            "consecutive invalidations must not reuse a CallerReference",
        )
        for distro, batch in invalidations:
            self.assertEqual(distro, "TESTDIST")
            self.assertIsInstance(batch["CallerReference"], str)
            self.assertEqual(
                batch["Paths"]["Items"],
                ["/loza-contract.json", "/manifest.json"],
            )
            # The reference is a UUID string (valid, canonical UUID form).
            self.assertEqual(str(uuid.UUID(batch["CallerReference"])), batch["CallerReference"])


if __name__ == "__main__":
    unittest.main()

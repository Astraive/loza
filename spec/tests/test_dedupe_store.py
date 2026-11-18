"""Regression tests for MASS-0851 / issue #940.

A configured-but-unreachable Redis backend must not silently downgrade to local,
per-process dedupe, because every collector instance would then accept the same event.
"""
import os
import sys
import types
import unittest
from pathlib import Path
from unittest import mock

repo_root = Path(__file__).resolve().parents[2]
spec_dir = repo_root / 'spec'
sys.path.insert(0, str(spec_dir))

from services.dedupe_store import (  # noqa: E402
    ALLOW_LOCAL_FALLBACK_ENV,
    LocalDedupeStore,
    RedisDedupeStore,
    RedisUnavailableError,
    build_store,
)


def _fake_redis_module(from_url):
    module = types.ModuleType('redis')
    module.from_url = from_url
    return module


class TestBuildStoreRedisFailure(unittest.TestCase):
    def setUp(self):
        # A REDIS_URL is configured for every test; the explicit opt-in is not.
        self._env = mock.patch.dict(
            os.environ,
            {'REDIS_URL': 'redis://127.0.0.1:6379/0'},
            clear=False,
        )
        self._env.start()
        self.addCleanup(self._env.stop)
        os.environ.pop(ALLOW_LOCAL_FALLBACK_ENV, None)

    @staticmethod
    def _patch_redis(from_url):
        return mock.patch.dict(sys.modules, {'redis': _fake_redis_module(from_url)})

    def test_redis_failure_fails_closed_when_redis_url_configured(self):
        original = ConnectionError('connection refused')
        from_url = mock.Mock(side_effect=original)
        with self._patch_redis(from_url):
            with self.assertRaises(RedisUnavailableError) as ctx:
                build_store()
        self.assertIs(ctx.exception.__cause__, original)
        from_url.assert_called_once_with(
            'redis://127.0.0.1:6379/0', decode_responses=True
        )

    def test_redis_failure_is_logged(self):
        from_url = mock.Mock(side_effect=ConnectionError('auth failed'))
        with self._patch_redis(from_url):
            with self.assertLogs('services.dedupe_store', level='ERROR') as logs:
                with self.assertRaises(RedisUnavailableError):
                    build_store()
        self.assertTrue(any('REDIS_URL' in line for line in logs.output))

    def test_explicit_opt_in_allows_local_fallback_with_warning(self):
        os.environ[ALLOW_LOCAL_FALLBACK_ENV] = '1'
        from_url = mock.Mock(side_effect=ConnectionError('connection refused'))
        with self._patch_redis(from_url):
            with self.assertLogs('services.dedupe_store', level='WARNING') as logs:
                store, backend = build_store()
        self.assertIsInstance(store, LocalDedupeStore)
        self.assertEqual(backend, 'local')
        self.assertTrue(any('per-process' in line for line in logs.output))

    def test_falsey_opt_in_still_fails_closed(self):
        os.environ[ALLOW_LOCAL_FALLBACK_ENV] = 'false'
        from_url = mock.Mock(side_effect=ConnectionError('connection refused'))
        with self._patch_redis(from_url):
            with self.assertRaises(RedisUnavailableError):
                build_store()

    def test_no_redis_url_uses_local_without_error(self):
        os.environ.pop('REDIS_URL', None)
        store, backend = build_store()
        self.assertIsInstance(store, LocalDedupeStore)
        self.assertEqual(backend, 'local')

    def test_healthy_redis_is_used(self):
        client = mock.Mock()
        client.ping.return_value = True
        from_url = mock.Mock(return_value=client)
        with self._patch_redis(from_url):
            store, backend = build_store()
        self.assertIsInstance(store, RedisDedupeStore)
        self.assertEqual(backend, 'redis')
        self.assertIs(store.redis, client)
        client.ping.assert_called_once_with()


if __name__ == '__main__':
    unittest.main()

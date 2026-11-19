#!/usr/bin/env python3
"""Dedupe store prototype: Redis-backed with local fallback.

Provides a tiny interface for idempotency windows: mark_seen(event_id, ttl_seconds),
is_seen(event_id), and delete(event_id) to release a claim. Redis is preferred. If REDIS_URL
is not set, a local file-backed store using shelve is used. If REDIS_URL *is* set but Redis
is unreachable (or redis-py is unavailable), build_store() fails closed (raises) by default:
silently degrading to per-process local dedupe would let every collector instance accept the
same event. Operators can explicitly opt back into that legacy fallback by setting
DEDUPE_ALLOW_LOCAL_FALLBACK=1; either way the failure is logged.
"""
from __future__ import annotations

import logging
import os
import time
from contextlib import contextmanager
from pathlib import Path
from typing import Optional

try:
    import fcntl  # POSIX advisory file locking
except ImportError:  # pragma: no cover - non-POSIX platforms
    fcntl = None

logger = logging.getLogger(__name__)

# Opt-in escape hatch for the legacy, per-process local dedupe fallback when REDIS_URL is
# configured but unreachable. Accepts common truthy values (1/true/yes/on).
ALLOW_LOCAL_FALLBACK_ENV = "DEDUPE_ALLOW_LOCAL_FALLBACK"


class RedisUnavailableError(RuntimeError):
    """REDIS_URL is configured but the Redis dedupe backend could not be reached."""


def _local_fallback_opted_in() -> bool:
    """Return whether the operator explicitly enabled the local dedupe fallback."""
    return os.environ.get(ALLOW_LOCAL_FALLBACK_ENV, "").strip().lower() in {
        "1", "true", "yes", "on",
    }


class LocalDedupeStore:
    def __init__(self, path: str | Path = "./.dedupe_store") -> None:
        import shelve

        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._shelf_path = str(self.path)
        self._lock_path = str(self.path) + ".lock"

    @contextmanager
    def _exclusive_lock(self):
        """Serialize the shelve read-modify-write across processes.

        A shelve/dbm store is not safe for concurrent writers, and the
        check-then-set in ``mark_seen`` (and check-then-delete in ``is_seen``) is
        a time-of-check/time-of-use race when the fallback store is shared by
        multiple processes. An exclusive advisory lock on a dedicated lock file
        makes the whole critical section atomic across processes.
        """
        if fcntl is None:  # pragma: no cover - non-POSIX platforms
            yield
            return
        with open(self._lock_path, "a+") as lock_file:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
            try:
                yield
            finally:
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)

    def mark_seen(self, key: str, ttl: int) -> bool:
        import shelve

        with self._exclusive_lock():
            with shelve.open(self._shelf_path) as db:
                now = int(time.time())
                expiry = now + ttl
                if key in db:
                    val = db[key]
                    if val >= now:
                        return False
                db[key] = expiry
                return True

    def is_seen(self, key: str) -> bool:
        import shelve

        with self._exclusive_lock():
            with shelve.open(self._shelf_path) as db:
                now = int(time.time())
                if key in db:
                    expiry = db[key]
                    if expiry >= now:
                        return True
                    # expired
                    del db[key]
                return False

    def delete(self, key: str) -> None:
        """Release a previously claimed key so a later delivery can retry it."""
        import shelve

        with shelve.open(self._shelf_path) as db:
            if key in db:
                del db[key]


class RedisDedupeStore:
    def __init__(self, redis_client) -> None:
        self.redis = redis_client

    def mark_seen(self, key: str, ttl: int) -> bool:
        # SETNX with expiry: set if not exists, return True if set
        was_set = self.redis.set(key, "1", nx=True, ex=ttl)
        return bool(was_set)

    def is_seen(self, key: str) -> bool:
        return bool(self.redis.get(key))

    def delete(self, key: str) -> None:
        """Release a previously claimed key so a later delivery can retry it."""
        self.redis.delete(key)


def build_store() -> tuple[object, str]:
    """Return (store, backend) where backend is 'redis' or 'local'.

    With REDIS_URL configured, a Redis connection/auth/ping failure is fatal by default.
    Silently falling back to LocalDedupeStore would give each collector instance its own
    dedupe state, allowing the same event to be accepted and enqueued by all of them. Set
    DEDUPE_ALLOW_LOCAL_FALLBACK=1 to explicitly allow the legacy local fallback; the
    failure is always logged so operator-visible observability does not depend on the mode.
    """
    redis_url = os.environ.get("REDIS_URL")
    if redis_url:
        try:
            import redis

            client = redis.from_url(redis_url, decode_responses=True)
            # simple ping
            client.ping()
            return RedisDedupeStore(client), "redis"
        except Exception as exc:
            logger.error(
                "Redis dedupe backend configured via REDIS_URL is unavailable: %s",
                exc, exc_info=True,
            )
            if _local_fallback_opted_in():
                logger.warning(
                    "Falling back to local, per-process dedupe because %s is enabled; "
                    "duplicate events may be accepted by multiple collector instances.",
                    ALLOW_LOCAL_FALLBACK_ENV,
                )
                return LocalDedupeStore(), "local"
            raise RedisUnavailableError(
                "REDIS_URL is configured but the Redis dedupe backend is unavailable; "
                f"set {ALLOW_LOCAL_FALLBACK_ENV}=1 to explicitly allow local dedupe fallback"
            ) from exc
    # No REDIS_URL configured: local dedupe is the intentional default.
    return LocalDedupeStore(), "local"


if __name__ == "__main__":
    store, backend = build_store()
    print("Using backend:", backend)
    test_key = "evt-test-123"
    print("first mark_seen ->", store.mark_seen(test_key, 10))
    print("second mark_seen ->", store.mark_seen(test_key, 10))
    print("is_seen ->", store.is_seen(test_key))

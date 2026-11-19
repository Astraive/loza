from __future__ import annotations

import pytest

from loza.internal.jsonenc.string import truncate_utf8 as truncate_utf8_internal
from loza.jsonenc.string import truncate_utf8 as truncate_utf8_public


@pytest.mark.parametrize("truncate", [truncate_utf8_public, truncate_utf8_internal])
def test_truncate_utf8_rejects_negative_max_bytes(truncate) -> None:
    with pytest.raises(ValueError, match="non-negative"):
        truncate("abc", -1)
    with pytest.raises(ValueError, match="non-negative"):
        truncate("", -1)


@pytest.mark.parametrize("truncate", [truncate_utf8_public, truncate_utf8_internal])
def test_truncate_utf8_non_negative_limits_still_work(truncate) -> None:
    assert truncate("abc", 0) == ""
    assert truncate("abc", 2) == "ab"
    assert truncate("abc", 3) == "abc"
    assert truncate("abc", 4) == "abc"
    # 2-byte limit keeps only the first character of a 2-byte UTF-8 sequence.
    assert truncate("aébc", 2) == "a"

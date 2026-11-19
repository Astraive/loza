from __future__ import annotations

import pytest

from loza.internal.jsonenc.string import join_path as internal_join_path
from loza.jsonenc.string import join_path


@pytest.mark.parametrize("impl", [join_path, internal_join_path])
@pytest.mark.parametrize(
    ("parts", "expected"),
    [
        (["a", "...", "b"], "a.b"),
        (["..", "a"], "a"),
        (["a", ".."], "a"),
        (["a", "", "b"], "a.b"),
        (["..."], ""),
        (["a", ".", "..", "b"], "a.b"),
    ],
)
def test_join_path_drops_segments_that_are_empty_after_stripping(
    impl, parts: list[str], expected: str
) -> None:
    assert impl(parts) == expected

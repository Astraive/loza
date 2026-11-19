from __future__ import annotations

import importlib
import importlib.util
from typing import Any, Callable

import pytest

from loza.core import event as event_module
from loza.core import logger as logger_module

# The canonical "write a dotted path" helpers. The removed loza.core.dotkey
# module duplicated these but had drifted: its set_path() treated an empty key
# as a no-op (``parts = key.split(".") if key else []``), while the canonical
# implementations consistently write ``target[""]``. Keeping a second,
# divergent implementation is what these regressions guard against.
_SET_PATH_HELPERS: tuple[Callable[[dict[str, Any], str, Any], None], ...] = (
    event_module._set_path_direct,
    logger_module.Logger._set_path_direct,
)


def test_dead_dotkey_module_is_removed() -> None:
    """The dead, duplicate path-helper module must no longer be importable."""
    assert importlib.util.find_spec("loza.core.dotkey") is None
    with pytest.raises(ModuleNotFoundError):
        importlib.import_module("loza.core.dotkey")


@pytest.mark.parametrize("set_path", _SET_PATH_HELPERS)
def test_canonical_set_path_handles_empty_key(set_path: Callable[[dict[str, Any], str, Any], None]) -> None:
    """Canonical helpers keep one empty-key behavior: write target[""]."""
    target: dict[str, Any] = {}
    set_path(target, "", "value")
    assert target[""] == "value"


def test_canonical_get_and_delete_path_handle_empty_key() -> None:
    """Read/delete helpers agree with set on the empty-key edge case."""
    target: dict[str, Any] = {}
    event_module._set_path_direct(target, "", "value")
    assert event_module._get_path(target, "") == "value"
    assert logger_module.Logger._get_path(target, "") == "value"

    event_module._delete_path(target, "")
    assert "" not in target

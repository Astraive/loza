from __future__ import annotations

import loza
from loza.core import schema


class _NoDeepCopy:
    """Sentinel that fails loudly if it is ever deep-copied."""

    def __deepcopy__(self, memo: dict[int, object]) -> _NoDeepCopy:
        raise AssertionError("an unrelated attribute was deep-copied")


def _event() -> loza.EventContext:
    return loza.EventContext(service="checkout", params=loza.Params(event="test.event"))


def test_eventview_attr_copies_only_the_requested_value() -> None:
    ctx = _event()
    ctx.attrs["wanted"] = {"value": 1}
    # Would blow up the old implementation, which deep-copied the entire dict.
    ctx.attrs["unrelated"] = _NoDeepCopy()

    view = schema.EventView(ctx)
    assert view.attr("wanted") == {"value": 1}
    assert view.attr("wanted.value") == 1
    assert view.attr("missing") is None


def test_eventview_attr_returns_detached_copy_of_nested_value() -> None:
    ctx = _event()
    ctx.attrs["nested"] = {"value": 1}

    view = schema.EventView(ctx)
    result = view.attr("nested")
    result["value"] = 99

    # EventView is a read-only view: mutating the result must not change the event.
    assert ctx.attrs["nested"]["value"] == 1
    assert view.attr("nested.value") == 1

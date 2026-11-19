import json
from functools import cmp_to_key
from typing import Any


def _key_text(key: Any) -> str:
    """Return the text ``json.dumps`` uses for an object key."""
    if key is True:
        return "true"
    if key is False:
        return "false"
    if key is None:
        return "null"
    if isinstance(key, str):
        return key
    return str(key)


def _compare_keys(left: Any, right: Any) -> int:
    """Order two keys, falling back to their text form when types are mixed."""
    try:
        if left < right:
            return -1
        if right < left:
            return 1
        return 0
    except TypeError:
        left_text, right_text = _key_text(left), _key_text(right)
        return (left_text > right_text) - (left_text < right_text)


def _sorted_keys(value: Any) -> Any:
    """Recursively order mapping keys so mixed key types cannot abort encoding."""
    if isinstance(value, dict):
        items = [(key, _sorted_keys(item)) for key, item in value.items()]
        items.sort(key=cmp_to_key(lambda left, right: _compare_keys(left[0], right[0])))
        return dict(items)
    if isinstance(value, list):
        return [_sorted_keys(item) for item in value]
    if isinstance(value, tuple):
        return tuple(_sorted_keys(item) for item in value)
    return value


def encode(value: Any, pretty: bool = False) -> str:
    return json.dumps(
        value,
        indent=2 if pretty else None,
        separators=None if pretty else (",", ":"),
        sort_keys=True,
        allow_nan=False,
    # Keys are sorted explicitly above; json.dumps' own sort_keys compares raw
    # keys and raises on dicts mixing, for example, str and int keys.
    return json.dumps(
        _sorted_keys(value),
        indent=2 if pretty else None,
        separators=None if pretty else (",", ":"),
    )

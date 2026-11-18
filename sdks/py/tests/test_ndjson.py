from __future__ import annotations

from loza.utils.ndjson import decode_lines, encode_line


def test_decode_lines_round_trips_multiple_records() -> None:
    text = encode_line({"event": "a", "n": 1}) + encode_line({"event": "b", "n": 2})
    assert decode_lines(text) == [
        {"event": "a", "n": 1},
        {"event": "b", "n": 2},
    ]


def test_decode_lines_keeps_literal_u2028_u2029_inside_strings() -> None:
    # U+2028/U+2029 are >= 0x20, so JSON does not require escaping them and a
    # producer using ensure_ascii=False may emit them literally. They are not
    # NDJSON record delimiters and must not split a single valid record.
    text = '{"msg":"a\u2028b\u2029c"}\n{"msg":"second"}\n'
    assert decode_lines(text) == [
        {"msg": "a\u2028b\u2029c"},
        {"msg": "second"},
    ]


def test_decode_lines_splits_only_on_newline() -> None:
    # U+0085 (NEL) is another splitlines() separator that is >= 0x20 and thus
    # legal to leave unescaped inside a JSON string; it must be preserved too.
    text = '{"v":"a\u0085b"}\n'
    assert decode_lines(text) == [{"v": "a\u0085b"}]


def test_decode_lines_tolerates_crlf_and_blank_lines() -> None:
    text = '{"a":1}\r\n\r\n{"a":2}\r\n'
    assert decode_lines(text) == [{"a": 1}, {"a": 2}]

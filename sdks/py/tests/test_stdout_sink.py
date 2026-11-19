from __future__ import annotations

import io
import sys

from loza.sinks.stdout import StdoutSink


class _FalsyStringIO(io.StringIO):
    """A text stream that reports itself as falsy when tested for truthiness."""

    def __bool__(self) -> bool:
        return False


def test_stdout_sink_uses_falsy_custom_stream() -> None:
    stream = _FalsyStringIO()
    sink = StdoutSink(stream)

    assert sink._stream is stream
    sink.write("event")
    assert stream.getvalue().strip() == "event"


def test_stdout_sink_defaults_to_stdout_for_none() -> None:
    sink = StdoutSink(None)

    assert sink._stream is sys.stdout

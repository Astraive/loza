import json


def _reject_nonstandard_constant(token: str):
    raise ValueError(f"non-standard JSON constant is not allowed: {token}")


def encode_line(payload):
    return json.dumps(payload, separators=(",", ":"), allow_nan=False) + "\n"


def decode_lines(text: str):
    # NDJSON is delimited by "\n" only. str.splitlines() also splits on U+2028,
    # U+2029 and other separators that may legally appear inside a JSON string,
    # so splitting on them would corrupt otherwise valid records.
    return [json.loads(line) for line in text.split("\n") if line.strip()]
    return [json.loads(line, parse_constant=_reject_nonstandard_constant) for line in text.splitlines() if line.strip()]

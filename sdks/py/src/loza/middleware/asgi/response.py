class ResponseCapture:
    """Tracks the status code and accumulated body size of one ASGI response."""

    def __init__(self, status_code: int = 500) -> None:
        # 500 matches the middleware's pre-response fallback status.
        self.status_code = status_code
        self.bytes = 0

    def record(self, status_code: int | None = None, body_bytes: int = 0) -> None:
        if status_code is not None:
            self.status_code = status_code
        self.bytes += body_bytes

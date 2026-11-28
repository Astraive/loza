# Integrations

Python logging framework integrations for the LOZA SDK. Each integration bridges a popular Python logging library to the LOZA event lifecycle.

## Available Integrations

| Module | Framework | Description |
|--------|-----------|-------------|
| `loza.integrations.logging` | stdlib `logging` | Bridges Python standard library logging to LOZA events. |
| `loza.integrations.loguru` | Loguru | Bridges Loguru log records to LOZA events. |
| `loza.integrations.structlog` | structlog | Bridges structlog processors to LOZA events. |
| `loza.integrations.otel` | OpenTelemetry | Bridges OpenTelemetry spans/logs to LOZA events. |

## stdlib logging

```python
import logging
import loza
from loza.integrations.logging import LozaHandler

loza.configure(loza.dev("myapp"))

logger = logging.getLogger("myapp")
logger.addHandler(LozaHandler())
logger.setLevel(logging.INFO)

logger.info("User signed up", extra={"user.id": "u-123"})
```

The handler converts each log record into a LOZA event with the appropriate level, message, and any extra fields as attributes. Configure the service name on the LOZA config, not on the handler.

## Loguru

```python
from loguru import logger
import loza
from loza.integrations.loguru import sink

loza.configure(loza.dev("myapp"))
logger.add(sink(), level="INFO")

logger.info("Order placed", order_id="ord-456")
```

## structlog

```python
import structlog
import loza
from loza.integrations.structlog import processor

loza.configure(loza.dev("myapp"))
structlog.configure(
    processors=[
        processor(),
        structlog.dev.ConsoleRenderer(),
    ]
)

log = structlog.get_logger()
log.info("payment_processed", amount=99.99, currency="USD")
```

## OpenTelemetry

```python
from loza.integrations.otel import LozaSpanExporter

exporter = LozaSpanExporter(service="myapp")
# Use with OpenTelemetry SDK tracer provider
```

## Design Notes

- Integrations are optional; the core SDK does not depend on any logging framework.
- Each integration maps framework-specific concepts (log levels, span attributes) to LOZA canonical fields.
- Go-specific integration names (`slog`, `zap`, `zerolog`) are not shipped as Python modules.
- Integrations use the global default logger unless a custom Logger is provided.

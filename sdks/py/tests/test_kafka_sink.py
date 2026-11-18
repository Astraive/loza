from __future__ import annotations

from loza.sinks import kafka_sink


def test_kafka_sink_accepts_topic_and_service_together() -> None:
    """Regression: `service` must not be forwarded twice to HTTPBatchSink."""
    sink = kafka_sink(topic="orders", service="checkout")

    assert sink.service == "orders"


def test_kafka_sink_falls_back_to_service_when_topic_is_empty() -> None:
    sink = kafka_sink(topic="", service="checkout")

    assert sink.service == "checkout"


def test_kafka_sink_without_service_still_works() -> None:
    assert kafka_sink(topic="orders").service == "orders"
    assert kafka_sink().service == ""

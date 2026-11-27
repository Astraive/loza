# Connect Go SDK

Use the SDK HTTP batch sink to emit spec-compatible events to the collector.
`httpbatch.New` returns `(loza.Sink, error)`, so handle the error before adding
the sink to the configuration:

```go
cfg := loza.Production().WithService("checkout")
sink, err := httpbatch.New(httpbatch.Config{
	URL: "http://localhost:9308/ingest",
})
if err != nil {
	return err
}
cfg.Sinks = []loza.Sink{sink}
```

For a complete runnable application, see the [HTTP batch example](../../sdks/go/examples/httpbatch-to-collector/README.md).

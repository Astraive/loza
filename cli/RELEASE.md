# Release Process

## Versioning

The CLI follows semantic versioning, aligned with the collector and cortex releases. The current stable release is **v0.2.0**.

## Pre-release Checklist

1. Run the full test suite:

```bash
cd cli
go test ./... -race -count=1
go vet ./...
```

2. Verify all commands parse correctly:

```bash
go run ./cmd/loza --help
go run ./cmd/loza collector --help
go run ./cmd/loza schema --help
```

3. Run the dependency boundary conformance test:

```bash
go test ./... -run TestDependencyBoundary
```

4. Verify the defaults config file is up to date.

5. Update `CHANGELOG.md` with the new version and date.

## Build

```bash
cd cli
go build -o loza.exe ./cmd/loza
```

## Tagging

```bash
git tag -a cli/v0.2.0 -m "cli v0.2.0"
git push origin cli/v0.2.0
```

## Distribution

The CLI is a single binary. Distribute via:

- Direct download from GitHub releases
- `go install github.com/astraive/loza/cli/cmd/loza@latest`
- Build from source: `go build -o loza.exe ./cmd/loza`

### Release assets and verification

The `publish-cli` workflow publishes one archive per platform plus a
`<archive>.sha256` sidecar containing `<sha256>  <archive-name>` and an
SLSA build attestation. Both install scripts download the sidecar from the
same HTTPS GitHub release and verify the archive's SHA-256 before installing;
they refuse to install on a missing, malformed, or mismatched checksum. Do not
publish a CLI release without the `.sha256` sidecars — installers fail closed
when they are absent. `LOZA_REPO` is restricted to the official release
repository so an override cannot redirect downloads to a third party.

## Post-release

1. Verify `loza doctor` reports all checks passing.
2. Verify `loza status` connects to a running collector.
3. Test `loza query` and `loza tail` against a live instance.

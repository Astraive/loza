# Contributing to LOZA

## Development Setup

### Generated protobuf code (Go)

The generated Go protobuf files under `gen/go/loza/core/` are committed, so a
fresh checkout can build `collector` and `cortex` without a generation step.
The `make bootstrap` target installs or replaces the pinned `protoc` and Go
plugins, regenerates the files, and builds the generated Go module:

```bash
make bootstrap
```

When you change a `*.proto` file under `proto/loza/core/`, run the bootstrap
command and commit the updated generated files. To verify all seven committed
outputs match the sources, run:

```bash
make check-proto
```

The sync check fails if any generated file is missing or out of date. It restores
the committed files after regeneration, so it does not leave generated changes
in your working tree.

### Collector (Go)

```bash
cd collector
go mod download
go build ./...
go test ./...
```

### Cortex (Go)

```bash
cd cortex
go mod download
go build ./...
go test ./...
```

### Go SDK

```bash
cd sdks/go
go mod download
go build ./...
go test ./...
```

### Python SDK

```bash
cd sdks/py
pip install -e ".[dev]"
pytest
```

### Rust SDK

```bash
cd sdks/rs
cargo build
cargo test
```

### JavaScript SDK

```bash
cd sdks/js
bun install
bun run test
```

### CLI

```bash
cd cli
go mod download
go build ./...
go test ./...
```

### Spec and Conformance

```bash
cd spec
pip install -r requirements.txt
python conformance/runner.py
python conformance/verify.py
```

## Code Conventions

| Language | Formatter        | Linter             | Config Location         |
|----------|------------------|--------------------|-------------------------|
| Go       | `gofmt`          | `golangci-lint`    | `collector/.golangci.yml` |
| Python   | `ruff format`    | `ruff check`, `mypy` | `sdks/py/pyproject.toml` |
| Rust     | `rustfmt`        | `clippy`           | `sdks/rs/rustfmt.toml`  |
| JavaScript | `prettier`     | `eslint`           | `sdks/js/.eslintrc`     |

All Go code must pass `gofmt -s` and `golangci-lint run` before submission. CI enforces both.
Run `make fmt` to format all Go source files in place, and `make fmt-check` to verify
formatting without modifying files. `make fmt-check` is the same check enforced by the Go CI
workflows; it fails if any Go file is not gofmt-clean.

Python code uses `ruff` for formatting and linting with `mypy` for type checking. Configuration lives in `pyproject.toml`.

Rust code uses `rustfmt` with the project config and must pass `cargo clippy -- -D warnings`.

JavaScript code uses `prettier` for formatting and `eslint` for linting. Run `bun run lint` before committing.

## Documentation Conventions

- File names: `kebab-case.md` (e.g., `getting-started.md`, `sdk-comparison.md`)
- No emojis in any documentation file
- Every document must include:
  - Title as H1
  - Badges (if applicable)
  - Table of contents (for documents longer than 3 sections)
  - At least one mermaid diagram (for architecture or flow documents)
  - Tables for structured comparisons
  - Fenced code blocks with language tags for all code samples
- Keep lines under 120 characters where practical
- Use relative links for cross-references within the monorepo

## Testing

### Component Tests

Each component has its own test suite. Run them individually:

```bash
# Collector
cd collector && go test ./...

# Cortex
cd cortex && go test ./...

# Go SDK
cd sdks/go && go test ./...

# Python SDK
cd sdks/py && pytest

# Rust SDK
cd sdks/rs && cargo test

# JavaScript SDK
cd sdks/js && bun run test

# CLI
cd cli && go test ./...
```

### Conformance Tests

The conformance runner validates all 4 SDKs against the spec:

```bash
cd spec && python conformance/runner.py
```

The comprehensive verifier runs 105 subchecks across 12 categories:

```bash
cd spec && python conformance/verify.py
```

### Benchmarks

Run the full benchmark suite:

```bash
cd bench && ./run-all.sh
```

Individual component benchmarks:

```bash
cd collector && go test -bench=. ./...
cd sdks/go && go test -bench=. ./...
cd sdks/rs && cargo bench
```

## PR Process

### Branch Naming

- `feat/<component>/<short-description>` -- new features
- `fix/<component>/<short-description>` -- bug fixes
- `docs/<short-description>` -- documentation changes
- `refactor/<component>/<short-description>` -- code refactoring
- `test/<component>/<short-description>` -- test additions or fixes

Examples: `feat/collector/otlp-sink`, `fix/sdk-go/retry-backoff`, `docs/getting-started`

### CI Gates

Every pull request must pass:

1. **Tests**: Component-specific test suite
2. **Conformance**: `spec/conformance/runner.py` for SDK changes
3. **Benchmarks**: `bench/run-all.sh` for performance-sensitive changes
4. **Doc Lint**: Markdown lint and link checks
5. **Lint**: Language-specific linter (golangci-lint, ruff, clippy, eslint)
6. **Go formatting**: `make fmt-check` (gofmt) passes for all Go source files

### Review

- At least one approval required before merge
- All CI checks must be green
- Squash merge to `main`
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
  - `feat(collector): add OTLP sink`
  - `fix(sdk-go): correct retry backoff calculation`
  - `docs: update getting started guide`

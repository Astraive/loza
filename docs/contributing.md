# Contributing to LOZA

## Development Setup

### First-time bootstrap (required)

The generated Go protobuf code under `gen/go/loza/core/` is not committed. After
cloning, run one command from the repository root to install the pinned protobuf
tools (`protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`) and generate the code:
### Generated protobuf code (Go)

The generated Go protobuf files under `gen/go/loza/core/` are committed. A fresh
clone can therefore build `collector` and `cortex` immediately, with no code
generation step:

```bash
git clone https://github.com/Astraive/loza.git
cd loza
(cd collector && go build ./...)
(cd cortex && go build ./...)
```

When you change a `*.proto` file under `proto/loza/core/`, regenerate and commit
the updated files. Install `protoc` (for example `apt install protobuf-compiler`
on Debian/Ubuntu or `brew install protobuf` on macOS), then run the one-command
bootstrap, which installs the pinned Go plugins and regenerates the code:

```bash
make bootstrap
```

`make proto` performs the same generation and now installs missing tools itself,
so it also works from a fresh clone. Run `make bootstrap` again whenever the
`.proto` files under `proto/loza/core/` change. Verify generated code is in sync
with `make check-proto` (or `bash scripts/check-proto-sync.sh`); both work whether
or not generated files are already present.

Without this step, `go build ./...` in `collector` or `cortex` fails because
`github.com/astraive/loza/gen/go/loza/core` does not exist yet.
`make bootstrap` installs `protoc-gen-go@v1.36.11` and
`protoc-gen-go-grpc@v1.6.2` into your Go bin directory and runs `make proto`.
Buf users can generate the same output with `cd proto && buf generate` (see
`proto/buf.gen.yaml`). Verify that committed code is in sync with:

```bash
make check-proto
# or
bash scripts/check-proto-sync.sh
```

Both checks ignore the `protoc` version comment, so they work with any protoc
release and even if the generated files are temporarily absent.

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

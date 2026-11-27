# Scripts

CI and release scripts for the LOZA monorepo.

| Script | Description |
|--------|-------------|
| `ci-bench.sh` | CI entry point for benchmark workflow |
| `ci-conformance.sh` | CI entry point for conformance workflow |
| `check-workflow-security.py` | Fails on unpinned actions or broad top-level permissions |
| `fmt-check.sh` | Fails if any Go source file is not gofmt-clean (used by `make fmt-check` and Go CI) |
| `lint-docs.sh` | Runs doc link + kebab-case checks |
| `release-all.sh` | Orchestrates release for all components |
| `../tools/generate_openapi.sh` | Generates collector clients for Go, Python, Rust, and TypeScript Fetch using Docker |

Run `../tools/generate_openapi.sh` from any working directory. It requires Docker
and writes clients to `generated/openapi/{go,python,rust,typescript-fetch}`.
The OpenAPI Generator CLI image is pinned to `v7.12.0`. Set `OPENAPI_OUTPUT_DIR`
to change the output directory (used by the local regression test).

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

#!/usr/bin/env bash
# Generate OpenAPI clients for the repository's supported SDK languages.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="${OPENAPI_OUTPUT_DIR:-$REPO_ROOT/generated/openapi}"
GENERATOR_IMAGE="openapitools/openapi-generator-cli:v7.12.0"

if ! command -v docker >/dev/null 2>&1; then
    echo "OpenAPI generation requires Docker (https://docs.docker.com/get-docker/)" >&2
    exit 1
fi

mkdir -p "$OUTPUT_DIR"

SPEC_PATH="spec/openapi/collector.openapi.yaml"
if [[ ! -f "$REPO_ROOT/$SPEC_PATH" ]]; then
    echo "Missing OpenAPI source: $SPEC_PATH" >&2
    exit 1
fi

for language in go python rust typescript-fetch; do
    docker run --rm \
        -v "$REPO_ROOT:/local:ro" \
        -v "$OUTPUT_DIR:/output" \
        "$GENERATOR_IMAGE" generate \
        -i "/local/$SPEC_PATH" \
        -g "$language" \
        -o "/output/$language"
done

echo "OpenAPI clients generated in $OUTPUT_DIR"

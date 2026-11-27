.PHONY: bootstrap proto-tools proto lint clean check-proto fmt fmt-check

# Directories
PROTO_DIR := proto
GEN_DIR := gen/go
PROTO_FILES := $(wildcard $(PROTO_DIR)/loza/core/*.proto)
GEN_FILES := $(patsubst $(PROTO_DIR)/loza/core/%.proto,$(GEN_DIR)/loza/core/%.pb.go,$(PROTO_FILES)) \
             $(patsubst $(PROTO_DIR)/loza/core/%.proto,$(GEN_DIR)/loza/core/%_grpc.pb.go,$(PROTO_FILES))

# Make the Go-installed protoc plugins visible to protoc and to command -v.
GOBIN_DIR := $(shell go env GOPATH)/bin
GOPATH_INCLUDE := $(shell go env GOPATH)/include
export PATH := $(GOBIN_DIR):$(PATH)

# Pinned tool versions keep generated output reproducible in CI and locally.
PROTOC_VERSION := 34.1
PROTOC_GEN_GO_VERSION := 1.36.11
PROTOC_GEN_GO_GRPC_VERSION := 1.6.2

PROTO_INCLUDES := -I $(PROTO_DIR) -I /usr/include
ifneq ($(wildcard $(GOPATH_INCLUDE)/google/protobuf/timestamp.proto),)
PROTO_INCLUDES += -I $(GOPATH_INCLUDE)
endif

# Regenerate committed protobuf files with pinned tools and verify the generated
# Go module builds.
bootstrap:
	@./scripts/bootstrap.sh

# Install or replace protobuf tools so generation always uses the pinned versions.
proto-tools:
	@set -eu; \
		actual=""; \
		if command -v protoc >/dev/null 2>&1; then \
			actual="$$(protoc --version 2>/dev/null || true)"; \
		fi; \
		if [ "$$actual" != "libprotoc $(PROTOC_VERSION)" ]; then \
			os=$$(uname -s); \
			arch=$$(uname -m); \
			case "$$os/$$arch" in \
				Linux/x86_64) platform=linux-x86_64 ;; \
				Linux/aarch64) platform=linux-aarch_64 ;; \
				Darwin/x86_64) platform=osx-x86_64 ;; \
				Darwin/arm64|Darwin/aarch64) platform=osx-aarch_64 ;; \
				*) echo "Unsupported protoc bootstrap platform: $$os/$$arch" >&2; exit 1 ;; \
			esac; \
			echo "Installing protoc $(PROTOC_VERSION) for $$platform"; \
			tmp=$$(mktemp -d); \
			trap 'rm -rf "$$tmp"' EXIT; \
			curl -fsSL "https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-$$platform.zip" -o "$$tmp/protoc.zip"; \
			unzip -oq "$$tmp/protoc.zip" -d "$$(go env GOPATH)"; \
		fi
	@set -eu; \
		actual=""; \
		if command -v protoc-gen-go >/dev/null 2>&1; then \
			actual="$$(protoc-gen-go --version 2>/dev/null || true)"; \
		fi; \
		if [ "$$actual" != "protoc-gen-go v$(PROTOC_GEN_GO_VERSION)" ]; then \
			echo "Installing protoc-gen-go $(PROTOC_GEN_GO_VERSION)"; \
			go install google.golang.org/protobuf/cmd/protoc-gen-go@v$(PROTOC_GEN_GO_VERSION); \
		fi
	@set -eu; \
		actual=""; \
		if command -v protoc-gen-go-grpc >/dev/null 2>&1; then \
			actual="$$(protoc-gen-go-grpc --version 2>/dev/null || true)"; \
		fi; \
		if [ "$$actual" != "protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)" ]; then \
			echo "Installing protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)"; \
			go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v$(PROTOC_GEN_GO_GRPC_VERSION); \
		fi

# Generate Go protobuf code; tracked outputs stay part of clean checkouts.
proto: proto-tools
	@mkdir -p $(GEN_DIR)/loza/core
	protoc $(PROTO_INCLUDES) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		proto/loza/core/event.proto \
		proto/loza/core/ingest.proto \
		proto/loza/core/collector.proto \
		proto/loza/core/cortex.proto

# Format all Go source files in place.
fmt:
	gofmt -w .

# Fail if any Go source file is not gofmt-clean (used by CI).
fmt-check:
	bash scripts/fmt-check.sh

# Lint protobuf definitions (requires buf).
lint:
	cd $(PROTO_DIR) && buf lint

# Remove generated Go protobuf code (committed files; restore with `make proto`).
clean:
	rm -rf $(GEN_DIR)/loza/core/*.pb.go $(GEN_DIR)/loza/core/*_grpc.pb.go

# Check that committed generated proto files match the proto sources.
# The protoc version comment is ignored because it varies across compiler releases.
check-proto:
	@bash scripts/check-proto-sync.sh

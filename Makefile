.PHONY: bootstrap proto-tools proto lint clean check-proto

# Directories
PROTO_DIR := proto
GEN_DIR := gen/go
PROTO_FILES := $(wildcard $(PROTO_DIR)/loza/core/*.proto)
GEN_FILES := $(patsubst $(PROTO_DIR)/loza/core/%.proto,$(GEN_DIR)/loza/core/%.pb.go,$(PROTO_FILES)) \
             $(patsubst $(PROTO_DIR)/loza/core/%.proto,$(GEN_DIR)/loza/core/%_grpc.pb.go,$(PROTO_FILES))

# Generated Go code lives in gen/go but is not committed. The bootstrap target
# below is the single documented command that makes a fresh clone buildable.
# Make the Go-installed protoc plugins visible to protoc and to command -v.
GOBIN_DIR := $(shell go env GOPATH)/bin
GOPATH_INCLUDE := $(shell go env GOPATH)/include
export PATH := $(GOBIN_DIR):$(PATH)

# Pinned tool versions keep generated output reproducible in CI and locally.
PROTOC_VERSION := 34.1
PROTOC_GEN_GO_VERSION := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

PROTO_INCLUDES := -I $(PROTO_DIR) -I /usr/include
ifneq ($(wildcard $(GOPATH_INCLUDE)/google/protobuf/timestamp.proto),)
PROTO_INCLUDES += -I $(GOPATH_INCLUDE)
endif

# One-command bootstrap from a fresh clone: install missing tools, generate
# the protobuf Go code and verify the generated module builds.
bootstrap:
	@./scripts/bootstrap.sh

# Install the protobuf compiler and Go plugins only when they are missing.
proto-tools:
	@command -v protoc >/dev/null 2>&1 || { \
		echo "protoc not found; installing protoc $(PROTOC_VERSION)"; \
		tmp=$$(mktemp -d); \
		curl -fsSL "https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-linux-x86_64.zip" -o "$$tmp/protoc.zip"; \
		unzip -oq "$$tmp/protoc.zip" -d "$$(go env GOPATH)"; \
		rm -rf "$$tmp"; \
	}
	@command -v protoc-gen-go >/dev/null 2>&1 || { \
		echo "Installing protoc-gen-go $(PROTOC_GEN_GO_VERSION)"; \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION); \
	}
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || { \
		echo "Installing protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)"; \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION); \
	}

# Generate Go protobuf code from proto/loza/core/*.proto into gen/go/loza/core/.
# proto-tools is a prerequisite so this works on a fresh clone without any
# manual installation of protoc or the Go plugins.
proto: proto-tools
	@mkdir -p $(GEN_DIR)/loza/core
	protoc $(PROTO_INCLUDES) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		proto/loza/core/event.proto \
		proto/loza/core/ingest.proto \
		proto/loza/core/collector.proto \
		proto/loza/core/cortex.proto

# Lint protobuf definitions (requires buf)
lint:
	cd $(PROTO_DIR) && buf lint

# Clean generated Go protobuf code
clean:
	rm -rf $(GEN_DIR)/loza/core/*.pb.go $(GEN_DIR)/loza/core/*_grpc.pb.go

# Check that generated proto files are up to date with source.
# Works whether or not generated files are present: files are backed up when
# they exist, regenerated, and only then compared byte-by-byte.
check-proto:
	@echo "Checking generated proto files are up to date..."
	@mkdir -p /tmp/proto-backup
	@cp $(GEN_DIR)/loza/core/*.pb.go $(GEN_DIR)/loza/core/*_grpc.pb.go /tmp/proto-backup/ 2>/dev/null || true
	@$(MAKE) proto
	@for f in /tmp/proto-backup/*.pb.go; do \
		[ -e "$$f" ] || continue; \
		basename=$$(basename $$f); \
		if ! cmp -s "$$f" "$(GEN_DIR)/loza/core/$$basename"; then \
			echo "ERROR: $$basename is out of date. Run 'make proto' and commit the changes."; \
			rm -rf /tmp/proto-backup; \
			exit 1; \
		fi \
	done
	@rm -rf /tmp/proto-backup
	@echo "All generated proto files are up to date."

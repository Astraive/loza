.PHONY: bootstrap proto-tools proto lint clean check-proto
.PHONY: proto bootstrap lint clean check-proto
.PHONY: proto lint clean check-proto fmt fmt-check

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
# Pinned protoc Go plugin versions (keep in sync with gen/go/go.mod).
PROTOC_GEN_GO_VERSION := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

# Generate Go protobuf code from proto/loza/core/*.proto into gen/go/loza/core/.
# Uses protoc directly; buf users get the same output with `cd proto && buf generate`
# (see proto/buf.gen.yaml). Generated files are committed, so a fresh clone does
# not need to run this target in order to build collector and cortex.
# Format all Go source files in place.
fmt:
	gofmt -w .

# Fail if any Go source file is not gofmt-clean (used by CI).
fmt-check:
	bash scripts/fmt-check.sh

# Generate Go protobuf code from proto/loza/core/*.proto into gen/go/loza/core/
# Uses protoc directly (buf is optional, see proto/buf.gen.yaml for buf config)
proto:
	@mkdir -p $(GEN_DIR)/loza/core
	protoc $(PROTO_INCLUDES) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		proto/loza/core/event.proto \
		proto/loza/core/ingest.proto \
		proto/loza/core/collector.proto \
		proto/loza/core/cortex.proto

# One-command regeneration bootstrap: install the pinned Go plugins, then generate.
# protoc itself is a system prerequisite (see docs/contributing.md).
bootstrap:
	@echo "Installing protoc Go plugins..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	$(MAKE) proto

# Lint protobuf definitions (requires buf)
lint:
	cd $(PROTO_DIR) && buf lint

# Remove generated Go protobuf code (committed files; restore with `make proto`).
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
# Check that committed generated proto files match the proto sources.
# Regenerates into the working tree and compares against a backup. The protoc
# version comment is ignored so the check works with any protoc release. Handles
# the case where generated files are absent instead of failing on an unmatched glob.
check-proto:
	@echo "Checking generated proto files are up to date..."
	@backup=$$(mktemp -d); \
	trap 'rm -rf "$$backup"' EXIT; \
	found=0; \
	for f in $(GEN_DIR)/loza/core/*.pb.go; do \
		if [ -f "$$f" ]; then cp "$$f" "$$backup/"; found=1; fi; \
	done; \
	if [ "$$found" -eq 0 ]; then \
		echo "ERROR: no generated files in $(GEN_DIR)/loza/core; run 'make proto' and commit them."; \
		exit 1; \
	fi; \
	$(MAKE) proto || exit 1; \
	status=0; \
	for f in "$$backup"/*.pb.go; do \
		[ -f "$$f" ] || continue; \
		base=$$(basename "$$f"); \
		if [ ! -f "$(GEN_DIR)/loza/core/$$base" ]; then \
			echo "ERROR: $$base is missing after regeneration. Run 'make proto' and commit the changes."; \
			status=1; \
			continue; \
		fi; \
		sed '/^\/\/.*[[:space:]]protoc[[:space:]].*v[0-9]/d' "$$f" > "$$backup/old.norm"; \
		sed '/^\/\/.*[[:space:]]protoc[[:space:]].*v[0-9]/d' "$(GEN_DIR)/loza/core/$$base" > "$$backup/new.norm"; \
		if ! cmp -s "$$backup/old.norm" "$$backup/new.norm"; then \
			echo "ERROR: $$base is out of date. Run 'make proto' and commit the changes."; \
			status=1; \
		fi; \
	done; \
	if [ "$$status" -eq 0 ]; then echo "All generated proto files are up to date."; fi; \
	exit $$status

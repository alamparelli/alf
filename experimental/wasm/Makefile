# ALF WASM spike — minimal build and demo.
#
# Targets:
#   make setup          Install Go module deps for host + examples.
#   make build          Build the host binary.
#   make build-examples Build the two guest .wasm files (wasip1).
#   make demo-tool      Run the hello tool once.
#   make demo-app       Serve the hello app on http://127.0.0.1:8787
#   make demo           Run the tool demo.
#   make clean          Remove build artefacts.

BIN      := bin
HOST     := $(BIN)/alf-wasm-host
TOOL     := $(BIN)/tool-hello.wasm
APP      := $(BIN)/app-hello.wasm
DATA_DIR := ./_data
PORT     ?= 8787

GO ?= go

.PHONY: all setup build build-examples demo demo-tool demo-app clean check-go

all: build build-examples

check-go:
	@$(GO) version | awk '{print "[ok] using " $$0}'

setup: check-go
	$(GO) mod tidy
	@echo "[ok] dependencies resolved"

build: $(HOST)

$(HOST): $(shell find cmd internal -type f -name '*.go' 2>/dev/null)
	@mkdir -p $(BIN)
	$(GO) build -o $(HOST) ./cmd/alf-wasm-host
	@echo "[ok] built $(HOST)"

build-examples: $(TOOL) $(APP)

$(TOOL): examples/tool-hello/main.go sdk/go/alf/alf.go
	@mkdir -p $(BIN)
	GOOS=wasip1 GOARCH=wasm $(GO) build -o $(TOOL) ./examples/tool-hello
	@# Copy next to manifest so runtime can find it via relative entry.
	@cp $(TOOL) examples/tool-hello/tool-hello.wasm
	@echo "[ok] built $(TOOL) ($$(stat -f%z $(TOOL) 2>/dev/null || stat -c%s $(TOOL)) bytes)"

$(APP): examples/app-hello/main.go sdk/go/alf/alf.go
	@mkdir -p $(BIN)
	GOOS=wasip1 GOARCH=wasm $(GO) build -o $(APP) ./examples/app-hello
	@cp $(APP) examples/app-hello/app-hello.wasm
	@echo "[ok] built $(APP) ($$(stat -f%z $(APP) 2>/dev/null || stat -c%s $(APP)) bytes)"

demo: demo-tool

demo-tool: build build-examples
	@mkdir -p $(DATA_DIR)
	@echo ""
	@echo "=== demo-tool: running hello tool ==="
	./$(HOST) run --manifest examples/tool-hello/manifest.toml --data $(DATA_DIR)
	@echo ""
	@echo "=== running it a second time (counter should increment) ==="
	./$(HOST) run --manifest examples/tool-hello/manifest.toml --data $(DATA_DIR)

demo-app: build build-examples
	@mkdir -p $(DATA_DIR)
	@echo ""
	@echo "=== demo-app: serving on http://127.0.0.1:$(PORT) ==="
	@echo "    open http://127.0.0.1:$(PORT) in your browser"
	@echo "    ctrl-c to stop"
	@echo ""
	./$(HOST) serve \
		--manifest examples/app-hello/manifest.toml \
		--frontend examples/app-hello/frontend \
		--data $(DATA_DIR) \
		--port $(PORT)

clean:
	rm -rf $(BIN) $(DATA_DIR)
	rm -f examples/tool-hello/tool-hello.wasm examples/app-hello/app-hello.wasm
	@echo "[ok] cleaned"

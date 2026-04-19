# alf — top-level Make targets.
#
# The regression target is the safety net contract for milestone
# 0.7.9 (see technical/TEST-BASELINE.md). It mirrors the production
# build flags so memstore's FTS5 virtual table actually compiles.

.PHONY: regression regression-race regression-quick test coverage

GO ?= go

regression: ## Full regression suite with coverage — required before/after every 0.7.9 step.
	CGO_ENABLED=1 $(GO) test -tags fts5 -count=1 -coverprofile=technical/cover.out -covermode=atomic ./...
	@echo
	@echo "Coverage summary:"
	@$(GO) tool cover -func=technical/cover.out | tail -1

regression-race: ## Same suite under the race detector — surfaces a known orchestrator race (see TEST-BASELINE.md).
	CGO_ENABLED=1 $(GO) test -tags fts5 -race -count=1 ./...

regression-quick: ## Same test set, no coverage — for a fast local check.
	CGO_ENABLED=1 $(GO) test -tags fts5 -count=1 ./...

test: regression-quick ## Alias for regression-quick.

coverage: ## Print per-function coverage from the last regression run.
	@test -f technical/cover.out || { echo "run 'make regression' first"; exit 1; }
	@$(GO) tool cover -func=technical/cover.out

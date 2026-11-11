GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := $(CURDIR)/.bin/golangci-lint/$(GOLANGCI_LINT_VERSION)/golangci-lint
ENSURE_GOLANGCI_LINT = test -x "$(GOLANGCI_LINT)" || GOBIN="$(dir $(GOLANGCI_LINT))" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: generate-all lint fmt test ci

generate-all:
	bash scripts/generate-api.sh

lint:
	@$(ENSURE_GOLANGCI_LINT)
	"$(GOLANGCI_LINT)" run ./...

fmt:
	@$(ENSURE_GOLANGCI_LINT)
	"$(GOLANGCI_LINT)" fmt ./...

test:
	go test ./...

ci:
	sh scripts/setup-local-ci.sh

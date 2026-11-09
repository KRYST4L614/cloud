GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := $(CURDIR)/.bin/golangci-lint/$(GOLANGCI_LINT_VERSION)/golangci-lint

.PHONY: generate-all lint fmt lint-install

generate-all:
	bash scripts/generate-api.sh

$(GOLANGCI_LINT):
	GOBIN=$(dir $(GOLANGCI_LINT)) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint-install: $(GOLANGCI_LINT)

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

fmt: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) fmt ./...

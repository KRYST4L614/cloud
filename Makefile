GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := $(CURDIR)/.bin/golangci-lint/$(GOLANGCI_LINT_VERSION)/golangci-lint

CI_COMPOSE := docker compose --env-file deployments/gitlab/.env -f deployments/gitlab/compose.yml

.PHONY: generate-all lint fmt lint-install ci-up ci-register ci-down ci-status ci-password

generate-all:
	bash scripts/generate-api.sh

$(GOLANGCI_LINT):
	GOBIN=$(dir $(GOLANGCI_LINT)) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint-install: $(GOLANGCI_LINT)

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

fmt: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) fmt ./...

ci-up:
	$(CI_COMPOSE) up -d

ci-register:
	$(CI_COMPOSE) run --rm runner register \
		--url http://gitlab.lan:9080 --executor docker \
		--docker-image docker:29.9.3-cli \
		--docker-network-mode cloud-ci \
		--docker-volumes /var/run/docker.sock:/var/run/docker.sock \
		--docker-volumes /cache

ci-down:
	$(CI_COMPOSE) down

ci-status:
	$(CI_COMPOSE) ps

ci-password:
	$(CI_COMPOSE) exec -T gitlab cat /etc/gitlab/initial_root_password

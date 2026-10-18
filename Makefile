BINARY      := mixed-fleet-check
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
CHART       := charts/mixed-generation-storage
KUBE_VERSION ?= 1.34.0

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the CLI into bin/
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

.PHONY: install
install: ## go install the CLI
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/$(BINARY)

.PHONY: test
test: ## Run unit tests
	go test -race ./...

.PHONY: cover
cover: ## Run tests and print coverage
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format Go and Terraform sources
	gofmt -w .
	terraform fmt -recursive terraform/ 2>/dev/null || echo "terraform not installed, skipping"

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint
lint: vet lint-yaml lint-helm lint-tf lint-sh ## Run every linter

.PHONY: lint-yaml
lint-yaml: ## yamllint + kubeconform
	yamllint -c .yamllint.yaml .
	kubeconform -strict -summary -kubernetes-version $(KUBE_VERSION) \
		-skip ComputeClass,Kustomization manifests/ examples/

.PHONY: lint-helm
lint-helm: ## helm lint and render
	helm lint $(CHART)
	helm template t $(CHART) >/dev/null
	helm template t $(CHART) --set computeClass.enabled=true --set audit.enabled=true >/dev/null

.PHONY: lint-tf
lint-tf: ## terraform fmt and validate
	@command -v terraform >/dev/null 2>&1 || { echo "terraform not installed, skipping"; exit 0; }
	terraform -chdir=terraform fmt -check -recursive -diff
	terraform -chdir=terraform init -backend=false -input=false
	terraform -chdir=terraform validate

.PHONY: lint-sh
lint-sh: ## shellcheck
	@command -v shellcheck >/dev/null 2>&1 || { echo "shellcheck not installed, skipping"; exit 0; }
	shellcheck hack/*.sh

.PHONY: audit
audit: build ## Run the audit against the current kubectl context
	./bin/$(BINARY)

.PHONY: apply
apply: ## Apply the base manifests to the current context
	kubectl apply -k manifests/

.PHONY: docker
docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin dist coverage.out

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help message
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: lint
lint: ## Lint source code
	@echo "Linting source code..."
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.0
	@golangci-lint run

.PHONY: test
test: ## Run tests with coverage
	@go test -timeout 10m -race -cover -failfast ./...

.PHONY: build
build: ## Build plugin and sidecar binaries
	@CGO_ENABLED=0 go build -o /bin/cnpg-i-scale-to-zero-plugin cmd/plugin/plugin.go
	@CGO_ENABLED=0 go build -o /bin/cnpg-scale-to-zero-sidecar cmd/sidecar/sidecar.go

.PHONY: docker-build-plugin-dev
docker-build-plugin-dev: ## Build Docker image for the plugin
	@echo "Building plugin Docker image..."
	@docker build -f Dockerfile.plugin -t cnpg-i-scale-to-zero-plugin:dev .

.PHONY: docker-build-sidecar-dev
docker-build-sidecar-dev: ## Build Docker image for the sidecar
	@echo "Building sidecar Docker image..."
	@docker build -f Dockerfile.sidecar -t cnpg-scale-to-zero-sidecar:dev .

.PHONY: docker-build-dev
docker-build-dev: docker-build-plugin-dev docker-build-sidecar-dev ## Build both Docker development images

.PHONY: manifest
manifest: ## Generate Kubernetes manifest
	@echo "Generating Kubernetes manifest..."
	@if command -v kustomize >/dev/null 2>&1; then \
		cd kubernetes && kustomize build . > ../manifest.yaml; \
		echo "Manifest generated at manifest.yaml using kustomize"; \
	else \
		echo "kustomize not found, using pre-generated manifest.yaml"; \
		echo "To regenerate, install kustomize and run 'make manifest' again"; \
	fi

.PHONY: manifest-dev
manifest-dev: manifest ## Generate development Kubernetes manifest with local images
	@echo "Generating development Kubernetes manifest with local images..."
	@cp manifest.yaml manifest-dev.yaml
	@sed -i.tmp 's|image: ghcr.io/xataio/cnpg-i-scale-to-zero:main|image: cnpg-i-scale-to-zero-plugin:dev|g' manifest-dev.yaml
	@sed -i.tmp 's|Z2hjci5pby94YXRhaW8vY25wZy1pLXNjYWxlLXRvLXplcm8tc2lkZWNhcjptYWlu|Y25wZy1zY2FsZS10by16ZXJvLXNpZGVjYXI6ZGV2|g' manifest-dev.yaml
	@rm -f manifest-dev.yaml.tmp
	@echo "Development manifest generated at manifest-dev.yaml with local images:"
	@echo "  - Plugin image: cnpg-i-scale-to-zero-plugin:dev"
	@echo "  - Sidecar image: cnpg-scale-to-zero-sidecar:dev"

.PHONY: deploy
deploy: manifest ## Deploy the manifest to the current Kubernetes cluster
	@echo "Deploying manifest to Kubernetes..."
	@kubectl apply -f manifest.yaml
	@echo "Waiting for deployment to be ready..."
	@kubectl wait --for=condition=available --timeout=300s deployment/scale-to-zero -n cnpg-system

.PHONY: undeploy
undeploy: ## Remove the plugin from the current Kubernetes cluster
	@echo "Removing scale-to-zero plugin from Kubernetes..."
	@kubectl delete -f manifest.yaml --ignore-not-found=true

.PHONY: deploy-dev
deploy-dev: manifest-dev ## Deploy the development manifest to the current Kubernetes cluster
	@echo "Deploying development manifest to Kubernetes..."
	@kubectl apply -f manifest-dev.yaml
	@echo "Waiting for deployment to be ready..."
	@kubectl wait --for=condition=available --timeout=300s deployment/scale-to-zero -n cnpg-system

.PHONY: undeploy-dev
undeploy-dev: ## Remove the development plugin from the current Kubernetes cluster
	@echo "Removing scale-to-zero plugin from Kubernetes..."
	@kubectl delete -f manifest-dev.yaml --ignore-not-found=true

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	@rm -f /bin/cnpg-i-scale-to-zero-plugin
	@rm -f /bin/cnpg-scale-to-zero-sidecar
	@rm -f coverage

.PHONY: kind-load-sidecar
kind-load-sidecar: docker-build-sidecar ## Build and load Docker sidecar images into kind cluster
	@echo "Loading sidecar image into kind cluster..."
	@kind load docker-image cnpg-scale-to-zero-sidecar:dev || echo "Failed to load sidecar image (kind cluster may not exist)"

.PHONY: kind-load-plugin
kind-load-plugin: docker-build-plugin ## Build and load Docker plugin images into kind cluster
	@echo "Loading plugin image into kind cluster..."
	@kind load docker-image cnpg-i-scale-to-zero-plugin:dev || echo "Failed to load plugin image (kind cluster may not exist)"

.PHONY: kind-load
kind-load: docker-build ## Build and load Docker images into kind cluster
	@echo "Loading images into kind cluster..."
	@kind load docker-image cnpg-i-scale-to-zero-plugin:dev || echo "Failed to load plugin image (kind cluster may not exist)"
	@kind load docker-image cnpg-scale-to-zero-sidecar:dev || echo "Failed to load sidecar image (kind cluster may not exist)"

.PHONY: kind-deploy-dev
kind-deploy-dev: kind-load manifest-dev ## Build, load images to kind, and deploy development manifest
	@echo "Deploying development manifest to Kubernetes..."
	@kubectl apply -f manifest-dev.yaml
	@echo "Waiting for deployment to be ready..."
	@kubectl wait --for=condition=available --timeout=300s deployment/scale-to-zero -n cnpg-system

.PHONY: all
all: lint test build docker-build-dev ## Run all quality checks and build everything

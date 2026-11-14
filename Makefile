# Gjallarhorn developer commands. CONTAINER can be podman (default) or docker.
CONTAINER ?= podman
VERSION   ?= 0.1.0-dev

.PHONY: help test race cover bench fuzz fmt vet image dev-image dev-test spike spike-test console demo-image demo lab

help: ## List commands
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*## /\t/'

test: ## Run engine tests
	cd engine && go test ./...

race: ## Run engine tests with the race detector
	cd engine && CGO_ENABLED=1 go test -race -count=1 ./...

cover: ## Show engine test coverage
	cd engine && go test -cover ./...

bench: ## Run engine benchmarks
	cd engine && go test -run XXX -bench . -benchtime 2s ./...

fuzz: ## Fuzz chain-ticket verification for 60s
	cd engine && go test ./ticket -run XXX -fuzz FuzzVerify -fuzztime 60s

fmt: ## Format Go code
	cd engine && gofmt -w .

vet: ## Vet Go code
	cd engine && go vet ./...

image: ## Build Bifrost + the Gjallarhorn plugin
	$(CONTAINER) build -t gjallarhorn-bifrost --build-arg GJ_VERSION=$(VERSION) -f deploy/Containerfile .

dev-image: image ## Build the development image (adds a mock model and a sample config)
	$(CONTAINER) build -t gjallarhorn-dev -f deploy/dev/Containerfile .

dev-test: ## Run integration tests against the development image
	ENGINE=$(CONTAINER) bash deploy/dev/test.sh

spike: ## Build the Phase 1 feasibility spike
	$(CONTAINER) build -t gjallarhorn-spike -f deploy/spike/Containerfile deploy/spike

spike-test: ## Run the Phase 1 feasibility checks
	ENGINE=$(CONTAINER) bash deploy/spike/test.sh

console: ## Type-check, test and build the operator console
	cd console && npm ci && npm run typecheck && npm test && npm run build

demo-image: image ## Build the full demo (plugin, console, agents, tools, attack lab)
	$(CONTAINER) build -t gjallarhorn-demo -f deploy/demo/Containerfile .

demo: ## Start the demo and print the console address
	ENGINE=$(CONTAINER) bash deploy/demo/run-console.sh

lab: ## Run the attack lab and the co-pilot fire drill
	ENGINE=$(CONTAINER) bash deploy/demo/run-lab.sh

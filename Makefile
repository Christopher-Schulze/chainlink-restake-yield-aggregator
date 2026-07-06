.PHONY: all build test test-race lint vet security forge-test forge-test-invariant forge-test-fork forge-fuzz forge-gas forge-coverage forge-fmt forge-build slither halmos clean docker-build docker-run help

##@ Common
all: build test lint vet security forge-test

##@ Go
build: ## Build all Go binaries
	go build ./...

test: ## Run Go tests
	go test -count=1 ./...

test-race: ## Run Go tests with race detector
	go test -race -count=1 ./...

test-integration: ## Run Go integration tests (requires network)
	go test -tags=integration -count=1 ./internal/fetch/...

bench: ## Run Go benchmarks
	go test -bench=. -benchmem -benchtime=1s ./internal/aggregate/...

##@ Linting & Security
lint: ## Run golangci-lint
	golangci-lint run --timeout 120s ./...

vet: ## Run go vet
	go vet ./...

security: ## Run gosec security scan
	go run github.com/securego/gosec/v2/cmd/gosec@latest -severity medium -quiet ./...

vulncheck: ## Run govulncheck
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

##@ Solidity (Foundry)
forge-build: ## Build Solidity contracts
	forge build

forge-test: ## Run Solidity unit tests
	forge test

forge-test-invariant: ## Run Solidity invariant tests
	forge test --match-test invariant

forge-test-fork: ## Run Solidity mainnet fork tests (requires FORK_URL)
	forge test --match-contract MainnetForkTest --fork-url $(FORK_URL) -vv

forge-fuzz: ## Run Solidity fuzz tests
	forge test --match-test testFuzz

forge-gas: ## Generate gas report
	forge test --gas-report | tee docs/gas-report.txt

forge-coverage: ## Generate Solidity coverage
	forge coverage --report lcov

forge-fmt: ## Format Solidity code
	forge fmt

slither: ## Run Slither security analysis
	slither . --config slither.config.json

halmos: ## Run Halmos formal verification
	halmos --function check_ --solver-timeout-assertion 60

##@ Docker
docker-build: ## Build Docker image
	docker build -t restake-yield-ea --build-arg VERSION=$(shell git rev-parse --short HEAD) .

docker-run: ## Run Docker container locally
	docker run -d --name restake-yield-ea -p 8080:8080 restake-yield-ea

##@ Cleanup
clean: ## Clean build artifacts
	go clean -testcache
	rm -f coverage.out
	rm -f docs/gas-report.txt

##@ Help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

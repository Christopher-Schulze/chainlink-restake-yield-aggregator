# Contributing

## Development Setup

### Prerequisites
- Go 1.24+
- Foundry (for Solidity tests) — https://book.getfoundry.sh/getting-started/installation
- Docker (optional, for containerized builds)
- Slither (optional, for Solidity security scanning) — `pip3 install slither-analyzer`
- golangci-lint (optional, for Go linting) — https://golangci-lint.run/usage/install/

### Getting Started
```bash
git clone <repo-url>
cd restake-yield-ea
go build ./...
forge install   # one-time, pulls forge-std + openzeppelin-contracts
```

## Code Style

### Go
- Run `gofmt -s -w .` before committing
- Run `go vet ./...` — must be clean
- Run `golangci-lint run --timeout 120s ./...` — must be 0 issues
- Follow effective Go conventions: https://go.dev/doc/effective_go
- Table-driven tests preferred
- All public functions must have doc comments

### Solidity
- Run `forge fmt` before committing
- Follow the Solidity Style Guide: https://docs.soliditylang.org/en/latest/style-guide.html
- Use NatSpec comments for all public/external functions
- Use custom errors instead of require strings for gas efficiency

## Testing

### Unit tests (default)
```bash
go test -race -count=1 ./...
forge test
```

### Integration tests (require network access)
```bash
go test -tags=integration -run TestIntegration -v ./internal/fetch/...
```

### Invariant tests
```bash
forge test --match-test invariant
```

### Fuzz tests
```bash
go test -fuzz=FuzzEIP712DigestDeterminism -fuzztime=30s ./internal/security/...
go test -fuzz=FuzzEIP712SignatureRecovery -fuzztime=30s ./internal/security/...
go test -fuzz=FuzzEIP712DomainSeparation -fuzztime=30s ./internal/security/...
```

### Benchmarks
```bash
go test -bench=. -benchmem -benchtime=1s ./internal/aggregate/...
```

## Security Checks

### Pre-commit hook
```bash
cp scripts/pre-commit.sh .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

The hook runs: `go vet`, `go test -race`, `forge test`, and scans for hardcoded private keys.

### Manual security scanning
```bash
# Go security scan
go run github.com/securego/gosec/v2/cmd/gosec@latest -severity medium ./...

# Go vulnerability check
go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Solidity security scan
slither . --config slither.config.json

# Go lint
golangci-lint run --timeout 120s ./...
```

## Pull Request Process

1. Create a feature branch from `main`: `git checkout -b feature/your-feature`
2. Make your changes following the code style above
3. Ensure all tests pass: `go test -race ./...` and `forge test`
4. Ensure all lint checks pass: `golangci-lint run ./...` and `forge fmt --check`
5. Run security scans: `gosec ./...` and `slither . --config slither.config.json`
6. Commit with a clear message: `fix(security): bind oracle digest to report params via EIP-712`
7. Push and create a PR with a description of what changed and why

### Commit message conventions
- `fix(security):` — security fixes
- `fix:` — bug fixes
- `feat:` — new features
- `docs:` — documentation changes
- `refactor:` — code refactoring
- `test:` — test additions or changes
- `chore:` — maintenance tasks

## Reporting Security Issues

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, email the maintainer directly or use GitHub's private vulnerability
reporting feature. See [SECURITY.md](SECURITY.md) for the full security policy.

- **Response time**: within 48 hours
- **Disclosure**: coordinated disclosure after a fix is released

## Project Structure

See [README.md](README.md) for the full project layout overview.

Key directories:
- `cmd/server/` — HTTP server, Chainlink EA endpoint
- `internal/` — Go packages (aggregate, circuitbreaker, fetch, security, validation, etc.)
- `contracts/` — Solidity contracts (YieldVerifier, RestakeYieldOracle, YieldConsumerExample)
- `deploy/` — Kubernetes and Docker Compose deployment configs

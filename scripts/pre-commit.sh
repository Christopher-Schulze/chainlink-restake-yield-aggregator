#!/usr/bin/env bash
# Pre-commit hook: runs security checks before allowing a commit.
# Install: cp scripts/pre-commit.sh .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit
set -euo pipefail

echo "Running pre-commit security checks..."

# 1. Go vet
echo "  go vet..."
if ! go vet ./... 2>&1; then
  echo "FAIL: go vet found issues"
  exit 1
fi

# 2. Go test (race detector)
echo "  go test -race..."
if ! go test -race -count=1 ./... 2>&1; then
  echo "FAIL: go test -race failed"
  exit 1
fi

# 3. Forge test (if foundry is installed)
if command -v forge &>/dev/null; then
  echo "  forge test..."
  if ! forge test 2>&1; then
    echo "FAIL: forge test failed"
    exit 1
  fi
else
  echo "  forge test... (skipped, foundry not installed)"
fi

# 4. Check for common secret patterns
echo "  scanning for secrets..."
if git diff --cached --name-only | grep -E '\.(env|key|pem)$' >/dev/null 2>&1; then
  echo "FAIL: staged files contain potential secret files (.env, .key, .pem)"
  exit 1
fi

# 5. Check for private key patterns in staged Go/Solidity files
if git diff --cached --name-only | grep -E '\.(go|sol)$' >/dev/null 2>&1; then
  if git diff --cached | grep -iE '(private[_ ]?key|priv[_ ]?key)\s*[:=]\s*"[0-9a-fA-F]{64}"' >/dev/null 2>&1; then
    echo "FAIL: staged changes contain a hardcoded private key"
    exit 1
  fi
fi

echo "All pre-commit checks passed."

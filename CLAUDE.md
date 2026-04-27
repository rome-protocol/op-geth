# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is **Rome Protocol's fork of op-geth** (Optimism's fork of go-ethereum). It serves as the Execution Layer in the OP Stack architecture, with minimal changes to maintain Ethereum equivalence. The Consensus Layer is handled by `op-node` (separate repo).

- **Go module:** `github.com/ethereum/go-ethereum` (retains upstream module path)
- **Go version:** 1.22+ (toolchain 1.23.1)
- **Upstream geth:** v1.13.8 | **op-geth:** v0.1.0

## Build & Test Commands

```bash
make geth              # Build geth binary → ./build/bin/geth
make all               # Build all executables
make test              # Build all, then run full test suite
make lint              # Run golangci-lint
make devtools          # Install code generation tools (stringer, gencodec, protoc-gen-go, abigen)
make forkdiff          # Generate HTML diff against upstream geth
```

### Running tests directly

```bash
# Single package
go test ./core/...

# Single test by name
go test -run TestName -v ./core/

# With required build tags (matches CI)
go test -tags=ckzg,integrationtests -timeout=20m ./...

# Via CI runner (applies correct flags: -p 1, tags, timeout)
go run build/ci.go test              # all packages
go run build/ci.go test -race        # with race detector
go run build/ci.go test -coverage    # with coverage
go run build/ci.go test -v           # verbose
```

CI runs tests with `-p 1` (one package at a time) to avoid timeouts.

## Key OP Stack Modifications

The fork follows an architecture where changes are **minimal and isolated**. Key areas:

### Deposit Transactions
- `core/types/deposit_tx.go` — Deposit transaction type (system-injected L1→L2 txs)
- `core/state_transition.go` — Special handling for deposits: skips gas checks, different behavior pre/post Regolith

### L1 Cost / Data Availability Fees
- `core/types/rollup_cost.go` — L1 cost calculation with distinct fee models per upgrade (Bedrock → Regolith → Ecotone)
- Reads from L1Block predeploy at `0x4200000000000000000000000000000000000015`

### Superchain Configuration
- `core/superchain.go` — Loads OP Stack genesis from superchain-registry
- `params/superchain.go` — Chain config loading for supported OP Stack chains
- `params/config.go` — `OptimismConfig` struct, chain IDs (OP Mainnet: 10, OP Goerli: 420)

### Rome Protocol Extensions
- `core/footprint/` — Transaction state footprint tracking (API + Manager). Exposes RPC methods to query which storage slots/accounts a transaction touches. Used for parallelization analysis.
- `internal/ethapi/api.go` — Emulate API for transaction simulation
- Opcode-related modifications in `core/vm/` for Rome's execution model

### Upgrade Schedule
Bedrock → Regolith → Canyon (Shanghai) → Ecotone (Cancun) — each upgrade has feature flags checked throughout the codebase via `params/config.go` methods.

## CI/CD

- **CircleCI** (`.circleci/config.yml`): build, unit-test, lint, `go mod tidy` check, Docker release, daily upstream update check
- **GitHub Actions** (`.github/workflows/`): dispatches Docker image build to `rome-protocol/rome-rollup-clients` (which owns the nginx + genesis runtime image published to `romeprotocol/rollup-op-geth`), then triggers downstream test suite in `rome-protocol/tests`
- **Dependabot** (`.github/dependabot.yml`): weekly updates for Go modules, GitHub Actions, and Docker base images (Fridays). Note: `blst >= 0.3.12` is pinned/ignored until upstream op-geth bumps it (Go 1.22.0 compat)

## Linting

Config in `.golangci.yml`. Key enabled linters: goimports, gosimple, govet, ineffassign, misspell, unconvert, staticcheck, unused, bidichk.

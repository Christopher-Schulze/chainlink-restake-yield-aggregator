# Deployment Guide — Chainlink Node Integration

## Architecture

The External Adapter (EA) sits between the Chainlink node and the on-chain oracle contracts:

```
Chainlink Node → Bridge (EA HTTP endpoint) → EA (Go) → Oracle Contract (Solidity)
```

The EA fetches yield data from multiple providers, aggregates it, signs it with EIP-712, and returns it in the Chainlink EA response format. The Chainlink node picks up the result and submits it to the `RestakeYieldOracle` contract via a job spec.

## 1. Bridge Configuration

In the Chainlink node's bridge configuration, add a new bridge pointing to the EA:

```json
{
  "name": "restake-yield-ea",
  "url": "http://restake-yield-ea:8080",
  "method": "POST",
  "requestData": {
    "data": {}
  }
}
```

The EA accepts `POST /` with `{"id": "...", "data": {}}` and returns `{"jobRunID": "...", "status": "completed", "data": {"result": 0.045, ...}, "result": 0.045, "error": null}`.

## 2. Job Spec

### Flux Monitor job (TOML)

```toml
type = "fluxmonitor"
schemaVersion = 1
name = "restake-yield"
contractAddress = "0x..."  # RestakeYieldOracle address
feeds = [
  {
    bridge = "restake-yield-ea"
  }
]
threshold = 0.5      # 50% deviation threshold
precision = 8
pollTimer = { period = "1m" }
quietEthereum = { timeout = "30s" }
```

### OCR (Off-Chain Reporting) job (TOML)

```toml
type = "offchainreporting"
schemaVersion = 1
name = "restake-yield-ocr"
contractAddress = "0x..."  # RestakeYieldOracle address
p2pPeerID = "..."
p2pBootstrapPeers = [...]
isBootstrap = false
keyBundleID = "..."
transmitterAddress = "..."
observationTimeout = "10s"
blockchainTimeout = "20s"
reportingTimeout = "10s"
```

The EA's `data.result` field (top-level `result`) contains the aggregated APY in decimal form (e.g., 0.045 = 4.5%).

## 3. Oracle Contract Deployment

### 3.1 Deploy contracts

The oracle is deployed in two steps: first the `YieldVerifier` (the low-level
`ecrecover` checker), then `RestakeYieldOracle`, which references the verifier
and the EA's signer address in its constructor.

```bash
# 1. Deploy YieldVerifier
forge create contracts/src/YieldVerifier.sol:YieldVerifier \
  --rpc-url $RPC_URL --private-key $DEPLOY_KEY

# 2. Deploy RestakeYieldOracle(verifier, signer)
forge create contracts/src/RestakeYieldOracle.sol:RestakeYieldOracle \
  --constructor-args $VERIFIER_ADDRESS $SIGNER_ADDRESS \
  --rpc-url $RPC_URL --private-key $DEPLOY_KEY
```

The constructor grants `DEFAULT_ADMIN_ROLE` and `ADMIN_ROLE` to the deployer,
and `UPDATER_ROLE` to the initial `_signer`. The deployer can later grant
`ADMIN_ROLE` to additional addresses or rotate the signer.

### 3.2 Configure the oracle

After deployment, configure the oracle:

1. **Set the authorised signer** — this must match the EA's `SIGNING_PRIVATE_KEY`.
   `setSigner` revokes the old signer's `UPDATER_ROLE` and grants it to the new
   signer, so rotation is atomic:
```bash
cast send $ORACLE_ADDRESS "setSigner(address)" $SIGNER_ADDRESS \
  --rpc-url $RPC_URL --private-key $ADMIN_KEY
```

2. **Grant UPDATER_ROLE to the Chainlink node's transmitter address**:
```bash
cast send $ORACLE_ADDRESS "setUpdater(address,bool)" $NODE_TRANSMITTER_ADDRESS true \
  --rpc-url $RPC_URL --private-key $ADMIN_KEY
```

3. **Configure bounds** (optional — defaults are shown):
```bash
# minAPYBps = 0, maxAPYBps = 10000 (100%), maxDeviationBps = 500 (5%), stalenessThreshold = 3600s
cast send $ORACLE_ADDRESS "setMinAPYBps(uint256)" 0 \
  --rpc-url $RPC_URL --private-key $ADMIN_KEY
cast send $ORACLE_ADDRESS "setMaxAPYBps(uint256)" 10000 \
  --rpc-url $RPC_URL --private-key $ADMIN_KEY
cast send $ORACLE_ADDRESS "setMaxDeviationBps(uint256)" 500 \
  --rpc-url $RPC_URL --private-key $ADMIN_KEY
cast send $ORACLE_ADDRESS "setStalenessThreshold(uint256)" 3600 \
  --rpc-url $RPC_URL --private-key $ADMIN_KEY
```

## 4. Environment Variables

### Required for production

| Variable | Value | Notes |
|----------|-------|-------|
| `SIGNING_PRIVATE_KEY` | `0x...` | Must match the oracle's `authorisedSigner`. **Never use an ephemeral key in production.** |
| `PORT` | `8080` | EA HTTP port |
| `ENABLED_PROVIDERS` | `defillama,lido` | Comma-separated list of active providers |
| `AGGREGATION_MODE` | `weighted` | Aggregation strategy |
| `ENABLE_CIRCUIT_BREAKER` | `true` | Circuit breaker protection |
| `MAX_STALE_SECONDS` | `300` | Max age for fallback data |

### Recommended for production

| Variable | Value | Notes |
|----------|-------|-------|
| `ADMIN_TOKEN` | `<random-32-char-string>` | Bearer token for admin endpoints |
| `TRUSTED_PROXY` | `<proxy-ip>` | Trusted proxy IP for X-Forwarded-For |
| `LOG_FORMAT` | `json` | Structured logging for log aggregation |
| `OTEL_ENDPOINT` | `http://collector:4318` | OpenTelemetry collector endpoint |
| `METRICS_EXPORT_ENABLED` | `true` | Batch metrics export to webhook |

### Optional enterprise features

| Variable | Value | Notes |
|----------|-------|-------|
| `ENABLE_ENTERPRISE_FEATURES` | `true` | Master switch |
| `DATA_INTEGRITY_ENABLED` | `true` | EIP-712 payload signing |
| `VERIFICATION_REQUIRED` | `true` | Reject requests if signature invalid |
| `MULTICHAIN_ENABLED` | `true` | Multi-chain fan-out |
| `RATE_LIMIT_RPS` | `10` | Requests per second |

## 5. Docker Deployment

```bash
docker build -t restake-yield-ea --build-arg VERSION=$(git rev-parse --short HEAD) .

docker run -d \
  --name restake-yield-ea \
  -p 8080:8080 \
  -e SIGNING_PRIVATE_KEY=$SIGNING_PRIVATE_KEY \
  -e ENABLED_PROVIDERS=defillama,lido \
  -e AGGREGATION_MODE=weighted \
  -e ENABLE_CIRCUIT_BREAKER=true \
  -e MAX_STALE_SECONDS=300 \
  -e ADMIN_TOKEN=$ADMIN_TOKEN \
  -e LOG_FORMAT=json \
  --restart unless-stopped \
  restake-yield-ea
```

## 6. Kubernetes Deployment

```bash
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/servicemonitor.yaml  # if using Prometheus Operator
```

The manifest includes:
- 2 replicas with rolling updates
- Liveness (`/health`) and readiness (`/readyz`) probes
- ConfigMap for non-secret env vars
- Secret for `SIGNING_PRIVATE_KEY` and `ADMIN_TOKEN`
- Resource requests/limits (100m/64Mi → 500m/256Mi)
- ServiceMonitor for Prometheus scraping

### Production checklist for K8s
- [ ] Create Kubernetes Secret with `SIGNING_PRIVATE_KEY` and `ADMIN_TOKEN`
- [ ] Verify the signer address matches the oracle contract's `authorisedSigner`
- [ ] Verify the Chainlink node's transmitter has `UPDATER_ROLE`
- [ ] Configure `TRUSTED_PROXY` to the load balancer/ingress IP
- [ ] Set up Prometheus alerts (see below)
- [ ] Run `forge test` and `go test -race` in CI before deploying

## 7. Monitoring

### Prometheus metrics

The EA exposes Prometheus metrics at `/metrics`. Key metrics to alert on:

| Alert | Condition | Severity |
|-------|-----------|----------|
| EA down | `up{job="restake-yield-ea"} == 0` | Critical |
| High error rate | `rate(ea_requests_total{status="errored"}[5m]) > 0.1` | High |
| Circuit breaker open | `ea_circuit_breaker_state == 2` | High |
| All providers failing | `ea_provider_errors_total / ea_provider_requests_total > 0.5` | High |
| High latency | `histogram_quantile(0.95, ea_request_duration_seconds_bucket) > 2` | Medium |
| Stale data | `ea_stale_data_served_total > 0` | Medium |

### Grafana dashboard

A pre-built Grafana dashboard is in `deploy/grafana/dashboard.json`. It shows:
- Request latency (p50/p95) and throughput by status
- Aggregated APY and TVL over time
- Per-provider latency and error rates
- Circuit breaker state

### Local observability stack

```bash
docker compose up -d
# EA:          http://localhost:8080
# Prometheus:  http://localhost:9090
# Grafana:     http://localhost:3000  (admin / admin)
```

## 8. Health Checks

| Endpoint | Purpose | Expected response |
|----------|---------|-------------------|
| `GET /health` | Liveness | `200 OK` |
| `GET /readyz` | Readiness | `200 OK` (503 if no providers or circuit open) |
| `GET /status` | Status | JSON with uptime, providers, circuit state |
| `GET /circuit` | Circuit state | JSON with breaker state and last trip reason |

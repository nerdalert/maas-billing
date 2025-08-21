# Benchmarking Setup

This directory contains scripts and configurations for load testing the MaaS billing system with 500 users across two tiers.

## Files

- `maas-k6.js` - K6 load testing script (4 users)
- `maas-k6-500-users.js` - K6 load testing script for all 500 users
- `generate_users.py` - Python script to generate user secrets
- `500-user-secrets.yaml` - Kubernetes manifest with 500 user API key secrets
- `burst_1000.json` - Burst test configuration

## User Distribution

- **250 Free Users**: `freeuser1` through `freeuser250`
- **250 Premium Users**: `premiumuser1` through `premiumuser250`

## Setup Instructions

### 1. Install K6

Install K6 load testing tool following the official documentation:
https://grafana.com/docs/k6/latest/set-up/install-k6/

### 2. Generate User Secrets

```bash
python3 generate_users.py
```

This creates `500-user-secrets.yaml` with 500 Kubernetes secrets containing API keys for testing.

### 3. Apply User Secrets to Cluster

```bash
kubectl apply -f 500-user-secrets.yaml
```

### 4. Run Benchmark Tests

#### Small Scale (4 users)
```bash
k6 run maas-k6.js
```

#### Full Scale (500 users) - Burst Test
```bash
k6 run \
  -e API_URL="http://<INSERT_ENDPOINT>/v1/chat/completions" \
  -e MODEL_ID="simulator-model" \
  -e MODE="burst" \
  -e BURST_ITERS="1000" \
  -e BURST_VUS="20" \
  --summary-export=results-burst.json \
  maas-k6-500-users.js
```

#### Full Scale (500 users) - Soak Test
```bash
k6 run \
  -e API_URL="http://<INSERT_ENDPOINT>/v1/chat/completions" \
  -e MODEL_ID="simulator-model" \
  -e MODE="soak" \
  -e DURATION="1m" \
  -e RATE_FREE="5" \
  -e RATE_PREM="5" \
  --summary-export=results-soak.json \
  maas-k6-500-users.js
```

## Configuration

The K6 script supports environment variables:
- `API_URL` - Complete API endpoint URL (e.g., "http://endpoint/v1/chat/completions")
- `MODEL_ID` - Model identifier
- `MODE` - Test mode: "burst" or "soak"
- `BURST_ITERS` - Requests per user in burst mode
- `BURST_VUS` - Virtual users per scenario
- `DURATION` - Test duration for soak mode
- `RATE_FREE` - RPS for free users
- `RATE_PREM` - RPS for premium users

## User Secret Format

Each user secret follows this pattern:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: {user_id}-apikey
  namespace: llm
  labels:
    kuadrant.io/auth-secret: "true"
    app: llm-gateway
  annotations:
    kuadrant.io/groups: {free|premium}
    secret.kuadrant.io/user-id: {user_id}
stringData:
  api_key: {user_id}_key
```
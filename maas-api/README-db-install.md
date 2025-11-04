# MaaS Database-Driven Architecture - Working Installation Guide

This guide covers the **working implementation** of the MaaS (Model as a Service) platform with route-scoped AuthPolicy architecture that **eliminates the circular dependency** between gateway-level policies and the `/introspect` endpoint.

## 🎯 Architecture Overview - WORKING STATE

### Route-Scoped AuthPolicy Architecture ✅
- **Data Plane**: Route-specific APIKEY authentication → OAuth2 introspection → database validation
- **Control Plane**: HTTPRoute-specific JWT authentication → Keycloak OIDC → user context injection  
- **Introspection**: Accessible to Authorino without circular dependency

### Key Fix: Route-Scoped vs Gateway-Scoped Policies
- ❌ **Old Problem**: Gateway-level AuthPolicy blocked `/introspect` → circular dependency
- ✅ **New Solution**: Route-scoped AuthPolicy on specific HTTPRoutes → `/introspect` accessible

### Components
- **PostgreSQL**: Database with teams, users, policies, API keys
- **MaaS-API**: Go service with management REST API and OAuth2 introspection  
- **Authorino**: Route-scoped AuthPolicies (no gateway-level conflicts)
- **Keycloak**: OIDC provider for JWT authentication
- **VLLM Simulator**: Model serving endpoint for testing

---

## ⚡ Quick Installation & Validation

Run the complete end-to-end workflow validation:

```bash
# Quick validation (runs complete QUICKSTART workflow)
cd ~/maas-billing/rdbms-v1/maas-billing/deployment/kuadrant-openshift/maas-api
./test-api-workflow.sh
```

**Expected output**: Complete workflow from JWT → Team → Policy → User → API Key → Data Plane inference working perfectly.

## 🔄 Resetting the Database

Use these manual steps to return PostgreSQL to the seeded state defined in `postgres/06-init-database.sql`. This drops and recreates the `public` schema, so only run it when you want to wipe _all_ data.

```bash
# From repo root
cd deployment/kuadrant-openshift

# Pick the running Postgres pod
POD=$(kubectl -n maas-db get pod -l app=postgres -o jsonpath='{.items[0].metadata.name}')

# Drop the existing schema and recreate it
kubectl exec -n maas-db "$POD" -- \
  psql -U maas_user -d maas_billing \
  -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO maas_user; GRANT ALL ON SCHEMA public TO public;"

# Reapply schema + seed data
kubectl cp postgres/06-init-database.sql maas-db/$POD:/tmp/init-database.sql
kubectl exec -n maas-db "$POD" -- psql -U maas_user -d maas_billing -f /tmp/init-database.sql

# (Optional) clean up the uploaded file
kubectl exec -n maas-db "$POD" -- rm -f /tmp/init-database.sql || true
```

If you only need to clear specific tables (for example, API keys) without removing lookup data, use targeted `TRUNCATE ... RESTART IDENTITY CASCADE` statements instead of dropping the schema.

## 📋 Working Manifest Installation Order

### Prerequisites - Kuadrant Installation
```bash
# Install Kuadrant operator (if not already installed)
helm repo add kuadrant https://kuadrant.io/helm-charts
helm repo update
helm install kuadrant kuadrant/kuadrant-operator --version 1.3.0-alpha2 --create-namespace --namespace kuadrant-system

# Create Kuadrant instance to activate Authorino and Limitador
cat > /tmp/kuadrant-instance.yaml << 'EOF'
apiVersion: kuadrant.io/v1beta1
kind: Kuadrant
metadata:
  name: kuadrant
  namespace: kuadrant-system
spec: {}
EOF
kubectl apply -f /tmp/kuadrant-instance.yaml
```

### Core Infrastructure
```bash
# 0. Apply namespaces
kubectl apply -f ../00-namespaces.yaml

# 1. PostgreSQL Database
kubectl apply -f ../postgres/02-postgres-secret.yaml
kubectl apply -f ../postgres/03-postgres-statefulset.yaml

# Create postgres init ConfigMap if missing
kubectl create configmap postgres-init --from-file=../postgres/06-init-database.sql -n maas-db

# Wait for PostgreSQL to be ready and initialize database schema
kubectl wait --for=condition=ready pod/postgres-0 -n maas-db --timeout=300s
kubectl cp ../postgres/06-init-database.sql postgres-0:/tmp/init-database.sql -n maas-db
kubectl exec postgres-0 -n maas-db -- psql -U maas_user -d maas_billing -f /tmp/init-database.sql

# 2. Keycloak Identity Provider
kubectl apply -f ../keycloak/
kubectl rollout status deployment keycloak -n keycloak-system --timeout=120s

# Fix realm import if needed (realm may not exist initially)
kubectl delete job -n keycloak-system keycloak-realm-import 2>/dev/null || true
kubectl apply -f ../keycloak/04-realm-import-job.yaml
kubectl wait --for=condition=complete job/keycloak-realm-import -n keycloak-system --timeout=120s

# 3. Gateway Infrastructure
kubectl apply -f ../02-gateway-configuration.yaml
kubectl apply -f ../02a-openshift-routes.yaml

# 4. KServe Configuration and Security Context Constraints
kubectl apply -f ../01-kserve-config-openshift.yaml
kubectl apply -f ../02b-openshift-scc.yaml

# 5. Model Routing Domains
kubectl apply -f ../03-model-routing-domains.yaml
```

### MaaS-API Service
```bash
# Core service components
kubectl apply -f 01-rbac.yaml                    # Service account & RBAC
kubectl apply -f 02-maas-api-deployment.yaml  # Main Go service
kubectl apply -f 05-external-access.yaml         # Service exposure
kubectl apply -f 07-maas-api-routing.yaml    # HTTPRoute configuration
kubectl apply -f 08-reference-grant.yaml        # Cross-namespace access
kubectl apply -f 09-maas-api-route.yaml      # OpenShift route

# Wait for maas-api to be ready
kubectl rollout status deployment maas-api -n maas-db --timeout=120s
```

### 🔧 GitHub OAuth Integration (Optional)

**Prerequisites**: GitHub OAuth App created at https://github.com/settings/developers

#### Step 1: GitHub OAuth App Configuration
- Application name: `MaaS Keycloak Broker`
- Homepage URL: `https://keycloak.apps.maas2.octo-emerging.redhataicoe.com`
- Authorization callback URL: `https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/broker/github/endpoint`

#### Step 2: Install GitHub OAuth Integration
```bash
# Create GitHub credentials secret
kubectl -n keycloak-system create secret generic github-idp-credentials \
  --from-literal=GITHUB_CLIENT_ID="YOUR_CLIENT_ID" \
  --from-literal=GITHUB_CLIENT_SECRET="YOUR_CLIENT_SECRET"

# Apply updated Keycloak deployment with hostname configuration
kubectl apply -f ../keycloak/02-keycloak-deployment.yaml

# Apply realm configuration with GitHub IdP
kubectl apply -f ../keycloak/03-maas-realm-config.yaml

# Re-import realm with GitHub IdP configuration
kubectl delete job -n keycloak-system keycloak-realm-import 2>/dev/null || true
kubectl apply -f ../keycloak/04-realm-import-job.yaml
kubectl wait --for=condition=complete job/keycloak-realm-import -n keycloak-system --timeout=120s

# Apply updated control plane AuthPolicy with external issuer
kubectl apply -f 13-control-plane-auth-policy.yaml -n maas-db
```

Notes
- The import job now uses `bitnami/os-shell` (includes `curl`, `bash`, and `envsubst`) and substitutes `${GITHUB_CLIENT_ID}`/`${GITHUB_CLIENT_SECRET}` from the `github-idp-credentials` Secret.
- If the realm already exists, job logs may show `{"errorMessage":"Conflict detected. See logs for details"}`. This is expected and safe to ignore.
- The control-plane AuthPolicy normalizes roles injected into `X-MaaS-User-Roles` as a single string (`"maas-admin"` or `"maas-user"`) for compatibility with the maas-api service.

#### Step 3: Fix Authorino TLS Certificate Validation for External Keycloak
```bash
# Critical fix: Update Authorino volume mounts to trust OpenShift router certificates
cat > /tmp/authorino-fix.yaml << 'EOF'
apiVersion: operator.authorino.kuadrant.io/v1beta1
kind: Authorino
metadata:
  name: authorino
  namespace: kuadrant-system
spec:
  clusterWide: true
  healthz: {}
  listener:
    ports: {}
    tls:
      enabled: false
  metrics:
    deep: true
    port: 8080
  oidcServer:
    tls:
      enabled: false
  supersedingHostSubsets: true
  tracing:
    endpoint: ""
  volumes:
    items:
    # CRITICAL: Mount router CA in system locations where curl/TLS will find it
    - configMaps:
      - authorino-router-ca
      items:
      - key: ca-bundle.crt
        path: tls-ca-bundle.pem
      mountPath: /etc/pki/ca-trust/extracted/pem
      name: router-ca-system-pem
    - configMaps:
      - authorino-router-ca
      items:
      - key: ca-bundle.crt
        path: ca-bundle.crt
      mountPath: /etc/pki/tls/certs
      name: router-ca-system-tls
    # Keep existing SSL certs location for compatibility
    - configMaps:
      - authorino-router-ca
      items:
      - key: ca-bundle.crt
        path: ca-certificates.crt
      mountPath: /etc/ssl/certs
      name: router-ca-debian
    - configMaps:
      - authorino-router-ca
      items:
      - key: ca-bundle.crt
        path: cert.pem
      mountPath: /etc/ssl
      name: router-ca-alpine
EOF

# Apply the fix
kubectl apply -f /tmp/authorino-fix.yaml

# Restart Authorino to apply new volume mounts
kubectl rollout restart deployment/authorino -n kuadrant-system
kubectl rollout status deployment/authorino -n kuadrant-system --timeout=60s
```

#### Step 4: Verify GitHub OAuth Integration
```bash
# Test device authorization flow
DEV=$(curl -k -s -X POST -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=maas-client" -d "client_secret=maas-client-secret" \
  https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/protocol/openid-connect/auth/device)

# Open verification URL and login with GitHub
echo "$DEV" | jq -r .verification_uri_complete

# After GitHub authorization, poll for token
DEVICE_CODE=$(echo "$DEV" | jq -r .device_code)
TOKEN=$(curl -k -s -X POST -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=urn:ietf:params:oauth:grant-type:device_code" \
  -d "device_code=$DEVICE_CODE" -d "client_id=maas-client" -d "client_secret=maas-client-secret" \
  https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/protocol/openid-connect/token | jq -r .access_token)

# Test control plane access with GitHub token
curl -sS -H "Authorization: Bearer $TOKEN" "http://maas-api.db.apps.maas2.octo-emerging.redhataicoe.com/health"

# User-level endpoint (requires maas-user or maas-admin)
curl -sS -H "Authorization: Bearer $TOKEN" "http://maas-api.db.apps.maas2.octo-emerging.redhataicoe.com/teams"
```

### 🔑 Inference Services and Authentication Policies

#### ⚠️ CRITICAL: Policy Target Matching for CEL Expression Binding

**Kuadrant CEL bindings are scoped per topological path.** For policies to share CEL context (e.g., for TokenRateLimitPolicy to access `auth.identity.*` from AuthPolicy), **both policies MUST target the same resource level** (Gateway or HTTPRoute).

**Why This Matters:**
- Kuadrant builds CEL validators **per request path**: `Gateway → Listener → HTTPRoute → Service`
- The `auth` binding (providing `auth.identity.*`) is only added to paths where an AuthPolicy exists
- If AuthPolicy targets HTTPRoute but TokenRateLimitPolicy targets Gateway, they're on **different paths**
- Result: TokenRateLimitPolicy's CEL expressions cannot reference `auth.identity.*` → validation fails

**✅ CORRECT Configuration (Both Target Gateway)**:
```yaml
# AuthPolicy - targets Gateway
apiVersion: kuadrant.io/v1
kind: AuthPolicy
metadata:
  name: data-plane-auth-gateway
spec:
  targetRef:
    kind: Gateway              # ← Gateway level
    name: inference-gateway

# TokenRateLimitPolicy - targets Gateway
apiVersion: kuadrant.io/v1alpha1
kind: TokenRateLimitPolicy
metadata:
  name: gateway-token-rate-limits
spec:
  targetRef:
    kind: Gateway              # ← Same level = same path
    name: inference-gateway
  limits:
    team-blue:
      rates:
        - limit: 100000
          window: "1h"
      when:
        - predicate: auth.identity.groups.split(",").exists(g, g == "team-blue")  # ✅ Works!
      counters:
        - expression: auth.identity.userid  # ✅ Works!
```

**❌ BROKEN Configuration (Mismatched Targets)**:
```yaml
# AuthPolicy - targets HTTPRoute
spec:
  targetRef:
    kind: HTTPRoute            # ← HTTPRoute level
    name: vllm-simulator-db

# TokenRateLimitPolicy - targets Gateway
spec:
  targetRef:
    kind: Gateway              # ← Different level = different path
    name: inference-gateway
  limits:
    team-blue:
      when:
        - predicate: auth.identity.groups...  # ❌ ERROR: undeclared reference to 'auth'
```

**Key Insight**: In this deployment, we use **Gateway-level targeting** for both AuthPolicy and TokenRateLimitPolicy because:
1. Applies authentication/rate-limiting to ALL routes through the gateway
2. Ensures both policies share the same topological path
3. Makes `auth.identity.*` CEL bindings available in TokenRateLimitPolicy

```bash
# Create KServe service account (CRITICAL - required for InferenceServices)
kubectl create serviceaccount kserve-service-account -n maas-db

# Deploy VLLM runtime (required before InferenceServices)
kubectl apply -f ../../model_serving/vllm-latest-runtime-openshift.yaml

# Deploy inference services
kubectl apply -f ../../model_serving/vllm-simulator-kserve-openshift.yaml
kubectl apply -f ../../model_serving/deepseek-r1-simulator-kserve-openshift.yaml

# Deploy consolidated model routes
kubectl apply -f maas-db-models-http-routes.yaml
kubectl apply -f maas-db-models-routes.yaml

# OAuth2 credentials for Authorino introspection
kubectl apply -f 12-oauth2-credentials.yaml

# Control plane AuthPolicy (JWT for management endpoints)
kubectl apply -f 13-control-plane-auth-policy.yaml

# Data plane AuthPolicy (APIKEY for inference endpoints)
# IMPORTANT: This targets Gateway (same as TokenRateLimitPolicy below)
kubectl apply -f data-plane-introspect.yaml

# Token rate limiting (targets Gateway to match AuthPolicy path)
# IMPORTANT: Must target same resource as AuthPolicy for CEL binding access
kubectl apply -f 04-token-rate-limit-policy.yaml

# Wait for inference services to be ready
kubectl wait --for=condition=ready pod -l serving.kserve.io/inferenceservice=vllm-simulator -n maas-db --timeout=300s
```

### Validation Commands
```bash
# Check all components are running
kubectl get pods -n maas-db -n keycloak-system
kubectl get routes -n maas-db
kubectl get authpolicy -n maas-db

# Test complete workflow
./test-api-workflow.sh
```

---

## 🎯 Current Working Manifest Files

### ✅ Active Manifests (Apply These)
```
# Core Infrastructure
../00-namespaces.yaml             # Namespace definitions
../01-kserve-config-openshift.yaml # KServe configuration
../02-gateway-configuration.yaml  # Istio gateway configuration
../02a-openshift-routes.yaml      # OpenShift routes
../02b-openshift-scc.yaml         # Security Context Constraints (CRITICAL)
../03-model-routing-domains.yaml  # Model routing domains

# Database
../postgres/02-postgres-secret.yaml # PostgreSQL credentials
../postgres/03-postgres-statefulset.yaml # PostgreSQL deployment

# Identity Provider
../keycloak/ manifests            # Keycloak deployment and realm config

# MaaS-API Service
01-rbac.yaml                      # Service account and RBAC permissions
02-maas-api-deployment.yaml    # MaaS API service deployment
05-external-access.yaml           # Service exposure configuration
07-maas-api-routing.yaml       # HTTPRoute for maas-api service
08-reference-grant.yaml           # Cross-namespace access permissions
09-maas-api-route.yaml         # OpenShift route for external access

# Inference Services
../../model_serving/vllm-latest-runtime-openshift.yaml # VLLM runtime
../../model_serving/vllm-simulator-kserve-openshift.yaml # VLLM simulator
../../model_serving/deepseek-r1-simulator-kserve-openshift.yaml # DeepSeek-R1 simulator
maas-db-models-http-routes.yaml   # Consolidated HTTPRoutes for models
maas-db-models-routes.yaml        # Consolidated OpenShift Routes for models

# Authentication Policies
12-oauth2-credentials.yaml        # OAuth2 client credentials for Authorino
13-control-plane-auth-policy.yaml # Control plane AuthPolicy (JWT)
04-token-rate-limit-policy.yaml   # Token-based rate limiting (optional)
data-plane-introspect.yaml        # Includes data-plane-auth-simulator AuthPolicy
```

### ❌ Removed/Deprecated Manifests
```
10-data-plane-auth-policy.yaml   # OLD: Gateway-scoped (caused circular dependency)
05-api-key-secrets.yaml          # REMOVED: Static API keys
06-auth-policies-apikey.yaml     # REMOVED: Old gateway-level policy  
03-auth-policy.yaml              # REMOVED: Conflicting policy
10-maas-api-auth-override.yaml # REMOVED: Auth override
```

## 🔍 Architecture Details - Gateway-Scoped with TokenRateLimitPolicy

### Current Architecture: Gateway-Scoped AuthPolicy + TokenRateLimitPolicy

**✅ Current Architecture (Working)**:
```yaml
# Data plane AuthPolicy - Gateway-scoped
apiVersion: kuadrant.io/v1
kind: AuthPolicy
metadata:
  name: data-plane-auth-gateway
spec:
  targetRef:
    kind: Gateway        # ← Applied to entire gateway
    name: inference-gateway

# TokenRateLimitPolicy - Gateway-scoped (MUST match AuthPolicy target)
apiVersion: kuadrant.io/v1alpha1
kind: TokenRateLimitPolicy
metadata:
  name: gateway-token-rate-limits
spec:
  targetRef:
    kind: Gateway        # ← MUST match AuthPolicy for CEL binding access
    name: inference-gateway
  limits:
    team-blue:
      when:
        - predicate: auth.identity.groups.split(",").exists(g, g == "team-blue")
      counters:
        - expression: auth.identity.userid
```

### Why Gateway-Scoped Works with Introspection Endpoint

**Key Insight**: The `/introspect` endpoint is accessed **internally by Authorino** via in-cluster service endpoint (`http://maas-api.maas-db.svc.cluster.local/introspect`), which bypasses the Gateway entirely. Therefore:

1. **Gateway-scoped AuthPolicy** applies to external traffic through OpenShift routes
2. **Internal cluster traffic** (Authorino → maas-api) doesn't traverse the Gateway
3. **Result**: No circular dependency - Authorino can introspect API keys without authentication

### Result: Full Protection with CEL Binding Access ✅
- **Data plane**: Protected by Gateway-scoped APIKEY AuthPolicy with OAuth2 introspection
- **Control plane**: Protected by HTTPRoute-scoped JWT AuthPolicy on `maas-api-control-plane-route`
- **Token rate limiting**: Works with `auth.identity.*` CEL expressions (same path as AuthPolicy)
- **Introspection**: Accessible to Authorino via in-cluster endpoint without gateway interference

## 🚀 Deployed Services

**Working Endpoints:**
- **MaaS API**: `http://maas-api.db.apps.maas2.octo-emerging.redhataicoe.com`
- **VLLM Simulator**: `http://simulator.db.apps.maas2.octo-emerging.redhataicoe.com`
- **DeepSeek-R1 Simulator**: `http://deepseek-r1.apps.maas2.octo-emerging.redhataicoe.com`
- **PostgreSQL**: Internal `postgres.maas-db.svc.cluster.local`
- **Keycloak**: `https://keycloak.apps.maas2.octo-emerging.redhataicoe.com`

**Additional KServe Endpoints:**
- **VLLM Simulator (KServe)**: `http://vllm-simulator-maas-db.apps.maas2.octo-emerging.redhataicoe.com`
- **DeepSeek-R1 (KServe)**: `http://deepseek-r1-maas-db.apps.maas2.octo-emerging.redhataicoe.com`

## 📊 Validation Results

Run `./test-api-workflow.sh` for complete validation showing:

```
✅ JWT Authentication: WORKING
✅ Control Plane (JWT): WORKING  
✅ Team/Policy/User Management: WORKING
✅ API Key Generation: WORKING
✅ Data Plane (APIKEY): WORKING
✅ Route-scoped AuthPolicy Fix: WORKING
✅ Circular Dependency: ELIMINATED
```

## 🔧 Troubleshooting

### AuthPolicy Status Check
```bash
# Check AuthPolicy status
kubectl describe authpolicy -n maas-db | grep -A4 "Status:"
# Should show: Accepted: True, Enforced: True

# Verify AuthConfigs created
kubectl get authconfig -A
# Should show AuthConfigs in kuadrant-system namespace
```

### Common Issues & Solutions

**Issue**: InferenceServices exist but no predictor pods are created
```bash
# Check if KServe service account exists
kubectl get serviceaccount kserve-service-account -n maas-db
# If missing: kubectl create serviceaccount kserve-service-account -n maas-db

# Check if VLLM runtime is deployed
kubectl get servingruntime -n maas-db
# If missing: kubectl apply -f ../../model_serving/vllm-latest-runtime-openshift.yaml

# Delete and recreate InferenceServices to pick up new runtime/service account
kubectl delete inferenceservice vllm-simulator deepseek-r1 -n maas-db
kubectl apply -f ../../model_serving/vllm-simulator-kserve-openshift.yaml
kubectl apply -f ../../model_serving/deepseek-r1-simulator-kserve-openshift.yaml
```

**Issue**: Pods fail with "unable to validate against any security context constraint" and runAsUser errors
```bash
# Check recent events for SCC errors
kubectl get events -n maas-db --sort-by='.lastTimestamp' | grep -i "forbidden\|scc"

# Error example: runAsUser: Invalid value: 65534: must be in the ranges: [1000780000, 1000789999]
# Fix: Remove explicit runAsUser from InferenceService securityContext
# OpenShift will automatically assign a UID from the namespace's allowed range

# The deepseek-r1 and vllm-simulator manifests have been fixed to omit runAsUser
# If you encounter this with other models, edit the YAML to remove:
#   securityContext:
#     runAsUser: 65534  # ← Remove this line
#     runAsGroup: 65534 # ← Remove this line
```

**Issue**: Data plane returns "no healthy upstream"
```bash
# Check if simulator pods are running
kubectl get pods -n maas-db | grep predictor
# If no pods: Follow InferenceService troubleshooting above

# Check if simulator is responding to health checks
kubectl logs -n maas-db $(kubectl get pods -n maas-db -l serving.kserve.io/inferenceservice=vllm-simulator -o jsonpath='{.items[0].metadata.name}')
# Should show health check responses: "GET /health HTTP/1.1" 200
```

**Issue**: Keycloak realm doesn't exist (device flow returns null)
```bash
# Delete and recreate realm import job
kubectl delete job -n keycloak-system keycloak-realm-import
kubectl apply -f ../keycloak/04-realm-import-job.yaml
kubectl logs -n keycloak-system -l job-name=keycloak-realm-import --follow

# Test realm exists
curl -k -s https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/.well-known/openid-configuration | jq -r .issuer
# Should return: https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas
```

**Issue**: AuthPolicies show "Accepted: True, Enforced: False"
```bash
# Check if Kuadrant instance exists to activate Authorino
kubectl get kuadrant -A
# If missing: Apply Kuadrant instance from Prerequisites section above

# Check if Authorino is running
kubectl get pods -n kuadrant-system | grep authorino
# Should show authorino pod running
```

**Issue**: TokenRateLimitPolicy shows "ERROR: undeclared reference to 'auth'" in CEL validation
```bash
# Check if AuthPolicy and TokenRateLimitPolicy target the SAME resource level
kubectl get authpolicy data-plane-auth-gateway -n maas-db -o jsonpath='{.spec.targetRef}'
kubectl get tokenratelimitpolicy gateway-token-rate-limits -n maas-db -o jsonpath='{.spec.targetRef}'
# Both MUST show: {"group":"gateway.networking.k8s.io","kind":"Gateway","name":"inference-gateway"}

# If they don't match, update AuthPolicy to target Gateway:
kubectl apply -f data-plane-introspect.yaml

# Verify TokenRateLimitPolicy status changes to Enforced: True
kubectl get tokenratelimitpolicy gateway-token-rate-limits -n maas-db -o jsonpath='{.status.conditions[?(@.type=="Enforced")]}'
```

**Issue**: GitHub OAuth tokens fail with "missing openid connect configuration" in Authorino logs
```bash
# Check Authorino logs for TLS certificate errors
kubectl logs -n kuadrant-system -l app=authorino --tail=20 | grep -E "(tls|certificate|x509)"

# Verify Authorino can connect to external Keycloak
POD=$(kubectl get pods -n kuadrant-system -l app=authorino -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n kuadrant-system $POD -- curl -s https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/.well-known/openid-configuration | jq -r .issuer

# If curl fails with certificate errors, verify CA mount points:
kubectl exec -n kuadrant-system $POD -- ls -la /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
kubectl exec -n kuadrant-system $POD -- grep -c "BEGIN CERTIFICATE" /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
# Should return "2" (OpenShift router certificates)
```

**Issue**: `/introspect` returns 401 with APIKEY challenge
```bash
# Check if old gateway-scoped AuthPolicy still exists
kubectl get authpolicy gateway-auth-policy -n maas-db
# If exists: kubectl delete authpolicy gateway-auth-policy -n maas-db
```

**Issue**: Data plane returns "token is not active"  
```bash
# Ensure team has default_policy_id set
kubectl exec postgres-0 -n maas-db -- psql -U maas_user -d maas_billing -c \
  "SELECT name, default_policy_id FROM teams WHERE default_policy_id IS NULL;"
# Fix via control plane: PATCH /teams/{id} with {"default_policy_id": "policy-uuid"}
```

**Issue**: Control plane returns 401 without JWT
```bash
# Verify JWT token acquisition
JWT=$(curl -s -k -X POST "https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=alice&password=password123&grant_type=password&client_id=maas-client&client_secret=maas-client-secret" \
| jq -r .access_token)
echo "JWT: $JWT"
```

**Issue**: "Valid role required (maas-admin or maas-user)" when calling `/teams`
```bash
# 1) Ensure your token has roles
echo "$TOKEN" | cut -d. -f2 | base64 -d | jq '.realm_access.roles'

# 2) Ensure the control-plane AuthPolicy is applied and enforced
kubectl apply -f 13-control-plane-auth-policy.yaml -n maas-db
kubectl get authpolicy -n maas-db maas-control-plane -o yaml | rg -n "enforced|Accepted|True" || true

# 3) For GitHub users, verify IdP role mapper is present
#    The realm config includes a hardcoded-role mapper that assigns "maas-user" on first login.
#    Re-run the realm import job if needed and sign out/in via GitHub to refresh roles.
```

### Copy-Paste Testing Commands

After running `./test-api-workflow.sh`, use the generated API key:

```bash
# Introspect API key  
curl -sS -X POST 'http://maas-api.db.apps.maas2.octo-emerging.redhataicoe.com/introspect' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'token=YOUR_API_KEY_HERE' | jq

# Data-plane inference call
curl -sS 'http://simulator.db.apps.maas2.octo-emerging.redhataicoe.com/v1/chat/completions' \
  -H 'Authorization: APIKEY YOUR_API_KEY_HERE' \
  -H 'Content-Type: application/json' \
  -d '{"model":"simulator-model","messages":[{"role":"user","content":"Tell me about MaaS!"}],"max_tokens":100}' | jq
```

---

## 🎉 Success Criteria

✅ **Complete QUICKSTART-db.md workflow working**
✅ **Route-scoped AuthPolicy architecture implemented**
✅ **Circular dependency eliminated**
✅ **End-to-end validation passing**
✅ **Copy-paste commands available for testing**
✅ **GitHub OAuth integration with Kuadrant**
✅ **TLS certificate validation fix for Authorino**
✅ **Device Authorization Flow and Browser Flow working**

### GitHub OAuth Validation
After completing the GitHub OAuth integration steps:

```bash
# Test complete workflow with GitHub authentication
./end-to-end-test.sh  # Password authentication should work
./test-api-workflow.sh  # Complete API workflow should work

# Test GitHub OAuth device flow
# Follow Step 4 verification commands above

# Verify both authentication methods work:
# 1. Password-based: alice/password123 → JWT → Control plane access
# 2. GitHub-based: Device flow → GitHub → JWT → Control plane access
```

**This installation guide reflects the current working state of the MaaS platform with database-driven authentication, route-scoped AuthPolicies, and GitHub OAuth integration with Kuadrant TLS certificate validation fix.**

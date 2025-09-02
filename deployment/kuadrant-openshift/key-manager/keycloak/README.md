# Keycloak Deployment for MaaS Authentication

This directory contains the Keycloak deployment for the MaaS (Models as a Service) platform using JWT-based authentication with hardcoded groups that will eventually migrate to dynamic PostgreSQL teams.

## Architecture Overview

- **Current Phase**: Hardcoded Keycloak groups (`free-users`, `premium-users`, `enterprise-users`)
- **Future Phase**: Dynamic teams from PostgreSQL database via `/identity/lookup` endpoint
- **Client Access**: External clients need to reach Keycloak for JWT token requests
- **Internal Access**: Authorino validates JWTs using Keycloak's JWKS endpoint

## Prerequisites

- OpenShift cluster with admin access
- `kubectl` or `oc` CLI configured
- Cluster domain available (e.g., `apps.maas2.octo-emerging.redhataicoe.com`)

## Installation Steps

### 1. Deploy Keycloak Namespace
```bash
kubectl apply -f 01-keycloak-namespace.yaml
```

### 2. Deploy Keycloak Service
```bash
kubectl apply -f 02-keycloak-deployment.yaml
```
This creates:
- Keycloak deployment with development settings
- ClusterIP service on port 8080
- Admin credentials: `admin` / `admin123`

### 3. Deploy Realm Configuration
```bash
kubectl apply -f 03-maas-realm-config.yaml
```
This creates a ConfigMap with the `maas` realm configuration including:
- OIDC client: `maas-client` with secret `maas-client-secret`
- User groups: `free-users`, `premium-users`, `enterprise-users`
- Pre-configured users with credentials `password123`

### 4. Wait for Keycloak to be Ready
```bash
kubectl wait --for=condition=Ready pod -l app=keycloak -n keycloak-system --timeout=300s
```

### 5. Import Realm Data
```bash
kubectl apply -f 04-realm-import-job.yaml
```
This runs a Kubernetes job that:
- Waits for Keycloak to be accessible
- Gets admin token
- Imports the `maas` realm configuration
- Creates users and groups

### 6. Create External Access Route
```bash
kubectl apply -f 07-keycloak-route.yaml
```
This creates an OpenShift Route for external client access.

### 7. Verify Installation
```bash
# Check all components are running
kubectl get pods,svc,routes -n keycloak-system

# Check realm import job completed successfully  
kubectl get jobs -n keycloak-system
kubectl logs -n keycloak-system job/keycloak-realm-import
```

## Access Information

### External Access (for clients)
- **Keycloak Console**: `http://keycloak.apps.maas2.octo-emerging.redhataicoe.com`
- **Admin Console**: `http://keycloak.apps.maas2.octo-emerging.redhataicoe.com/admin`
- **Realm**: `maas`
- **OIDC Endpoints**: `http://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas`

### Internal Access (for Authorino)
- **Service**: `keycloak.keycloak-system.svc.cluster.local:8080`
- **JWKS URL**: `http://keycloak.keycloak-system.svc.cluster.local:8080/realms/maas/protocol/openid-connect/certs`
- **Issuer**: `http://keycloak.keycloak-system.svc.cluster.local:8080/realms/maas`

## Pre-configured Users

| Username | Password | Group | Tier | Rate Limit |
|----------|----------|-------|------|------------|
| `freeuser1` | `password123` | `free-users` | free | 5 req/2min |
| `freeuser2` | `password123` | `free-users` | free | 5 req/2min |
| `premiumuser1` | `password123` | `premium-users` | premium | 20 req/2min |
| `premiumuser2` | `password123` | `premium-users` | premium | 20 req/2min |
| `enterpriseuser1` | `password123` | `enterprise-users` | enterprise | 100 req/2min |

## Testing JWT Authentication

### Get JWT Token
```bash
# Get token for free user
./get-token.sh freeuser1

# Get token for premium user  
./get-token.sh premiumuser1

# Get token for enterprise user
./get-token.sh enterpriseuser1
```

### Manual Token Request
```bash
KEYCLOAK_URL="http://keycloak.apps.maas2.octo-emerging.redhataicoe.com"

# Get access token
TOKEN=$(curl -s -X POST "$KEYCLOAK_URL/realms/maas/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password" \
  -d "client_id=maas-client" \
  -d "client_secret=maas-client-secret" \
  -d "username=freeuser1" \
  -d "password=password123" | jq -r '.access_token')

echo "JWT Token: $TOKEN"
```

## Next Steps

1. **Update Authorino AuthPolicy** - Switch from API key to JWT authentication
2. **Test Authentication Flow** - Verify JWT validation works
3. **Implement PostgreSQL Integration** - Add `/identity/lookup` endpoint for dynamic teams
4. **Migration to Dynamic Teams** - Replace hardcoded groups with database-driven teams

## Troubleshooting

### Keycloak Pod Issues
```bash
# Check pod status
kubectl get pods -n keycloak-system

# Check logs
kubectl logs -n keycloak-system deployment/keycloak

# Check service
kubectl get svc -n keycloak-system
```

### Realm Import Issues
```bash
# Check import job logs
kubectl logs -n keycloak-system job/keycloak-realm-import

# Re-run import if needed
kubectl delete job -n keycloak-system keycloak-realm-import
kubectl apply -f 04-realm-import-job.yaml
```

### Route Access Issues
```bash
# Check route status
kubectl get routes -n keycloak-system

# Test external access
curl -I http://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas
```

## Configuration Files

- `01-keycloak-namespace.yaml` - Namespace creation
- `02-keycloak-deployment.yaml` - Keycloak deployment and service
- `03-maas-realm-config.yaml` - Realm configuration with users and groups
- `04-realm-import-job.yaml` - Job to import realm data
- `07-keycloak-route.yaml` - OpenShift route for external access
- `get-token.sh` - Script to get JWT tokens for testing
- `test-oidc-auth.sh` - Script to test OIDC authentication
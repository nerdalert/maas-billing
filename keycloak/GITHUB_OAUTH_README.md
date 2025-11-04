# GitHub OAuth Integration for MaaS Platform

This guide configures GitHub OAuth authentication for the MaaS platform, allowing users to "Login with GitHub" instead of creating separate Keycloak accounts.

## Overview

The integration:
- Adds GitHub as an Identity Provider (IdP) in the existing `maas` realm
- Maps GitHub users to Keycloak users automatically on first login
- Assigns new users to the `/free-users` group by default
- Preserves existing JWT token structure for seamless AuthPolicy compatibility
- Maintains your database-first architecture (JWT for control-plane, API keys for data-plane)

## Quick Setup

### 1. Create GitHub OAuth App

1. Go to: https://github.com/settings/developers
2. Click **OAuth Apps** → **New OAuth App**
3. Fill in:
   - **Application name**: `MaaS Platform`
   - **Homepage URL**: `https://keycloak.apps.maas2.octo-emerging.redhataicoe.com`
   - **Authorization callback URL**: `https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/broker/github/endpoint`
4. Click **Register application**
5. Copy the **Client ID** and generate a **Client Secret**

### 2. Run the Setup Script

```bash
cd /home/ubuntu/maas-billing/v5-base-db/maas-billing/deployment/kuadrant-openshift/keycloak
./setup-github-oauth.sh
```

The script will:
- Prompt for your GitHub Client ID and Secret
- Create the Kubernetes secret
- Deploy the GitHub IdP configuration
- Deploy the user mappers and default group assignment

### 3. Test the Integration

1. Open: https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/account
2. Click **Sign in** → **GitHub**
3. Authorize the app → you should be logged in as a new user

## Manual Setup (Alternative)

If you prefer manual setup:

### Create Kubernetes Secret
```bash
kubectl -n keycloak-system create secret generic github-oauth \
  --from-literal=clientId='YOUR_GITHUB_CLIENT_ID' \
  --from-literal=clientSecret='YOUR_GITHUB_CLIENT_SECRET'
```

### Deploy IdP Configuration
```bash
kubectl apply -f 08-github-idp-job.yaml
kubectl wait --for=condition=complete job/keycloak-github-idp -n keycloak-system --timeout=120s
```

### Deploy Mappers Configuration
```bash
kubectl apply -f 09-github-mappers-job.yaml
kubectl wait --for=condition=complete job/keycloak-github-mappers -n keycloak-system --timeout=120s
```

## What Gets Configured

### GitHub Identity Provider
- **Provider ID**: `github`
- **Trust Email**: Enabled (GitHub verified emails are trusted)
- **Store Token**: Enabled (GitHub tokens are stored for potential API calls)
- **Scopes**: `read:user user:email` (basic profile and email access)

### User Mappers
- **Groups Mapper**: Maps Keycloak groups to `groups` claim in JWT tokens
- **Tier Mapper**: Maps user `tier` attribute to `tier` claim in JWT tokens
- **Default Group**: New users are automatically added to `/free-users` group

### JWT Token Claims (Unchanged)
Your existing AuthPolicy continues to work because tokens still contain:
- `sub`: User's Keycloak UUID
- `preferred_username`: GitHub login name
- `email`: GitHub primary email
- `groups`: Keycloak group memberships (starts with `/free-users`)
- `tier`: User's tier attribute (can be set via Admin API later)

## User Flow

1. **First Login**:
   - User clicks "Login with GitHub" → redirected to GitHub OAuth
   - GitHub asks permission for basic profile and email
   - User approves → redirected back to Keycloak
   - Keycloak auto-creates user account with GitHub details
   - User is assigned to `/free-users` group
   - JWT token is issued with standard claims

2. **Subsequent Logins**:
   - User clicks "Login with GitHub" → direct login (no approval needed)
   - Existing Keycloak user account is used
   - JWT token issued with current group/tier assignments

## Backend Integration

Your existing backend code works unchanged:

### Control Plane Operations
```bash
# Admin automation (unchanged)
JWT=$(./get-token.sh)

# User operations with GitHub-issued tokens
curl -H "Authorization: Bearer $USER_JWT" "$CONTROL_BASE/teams"
```

### Team/User Management
```bash
# After user logs in via GitHub, backend can manage their tier/groups
curl -X PUT "$CONTROL_BASE/users/$USER_ID" \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -d '{"tier": "premium"}'
```

## AuthPolicy Compatibility

Your existing AuthPolicy at `/home/ubuntu/maas-billing/v5-base-db/maas-billing/deployment/kuadrant-openshift/keycloak/05-auth-policy-oidc.yaml` continues to work because:

- **Same issuer**: `https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas`
- **Same claims**: `auth.identity.sub`, `auth.identity.groups`, `auth.identity.tier[0]`, etc.
- **Same JWKS endpoint**: `/.well-known/openid_configuration`

## Troubleshooting

### Check Job Status
```bash
kubectl get jobs -n keycloak-system | grep github
kubectl logs job/keycloak-github-idp -n keycloak-system
kubectl logs job/keycloak-github-mappers -n keycloak-system
```

### Verify IdP Configuration
```bash
# Get admin token
ADMIN_TOKEN=$(curl -ks -X POST \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=admin&password=admin123&grant_type=password&client_id=admin-cli" \
  "https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/master/protocol/openid-connect/token" | \
  jq -r .access_token)

# Check GitHub IdP
curl -ks -H "Authorization: Bearer $ADMIN_TOKEN" \
  "https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/admin/realms/maas/identity-provider/instances/github"
```

### Common Issues

1. **"Invalid redirect URI"**: Ensure the GitHub OAuth app's callback URL exactly matches the Keycloak broker endpoint
2. **"Client ID not found"**: Verify the Kubernetes secret was created correctly
3. **New users not in groups**: Check the mappers job completed successfully

## Security Notes

- GitHub tokens are stored in Keycloak (for potential future GitHub API calls)
- User email trust is enabled (GitHub verifies emails)
- Default scope is minimal (`read:user user:email`)
- No additional permissions beyond basic profile are requested

## Next Steps

Consider these enhancements:
- **GitHub Organization/Team Mapping**: Map GitHub org membership to Keycloak groups/tiers
- **Device Authorization Flow**: Allow CLI-based GitHub login for automation
- **Group-based Access Control**: Use GitHub team membership to control model access
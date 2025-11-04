#!/bin/bash

set -euo pipefail

echo "🔧 GitHub OAuth Setup for Keycloak MaaS Platform"
echo "================================================"
echo

# Check if we're in the right directory
if [[ ! -f "08-github-idp-job.yaml" ]]; then
    echo "❌ Error: Please run this script from the keycloak/ directory"
    exit 1
fi

echo "📋 Step 1: Create GitHub OAuth App"
echo "1. Go to: https://github.com/settings/developers"
echo "2. Click 'OAuth Apps' → 'New OAuth App'"
echo "3. Fill in:"
echo "   - Application name: MaaS Platform"
echo "   - Homepage URL: https://keycloak.apps.maas2.octo-emerging.redhataicoe.com"
echo "   - Authorization callback URL: https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/broker/github/endpoint"
echo "4. Click 'Register application'"
echo "5. Copy the Client ID and generate a Client Secret"
echo

read -p "📝 Enter GitHub Client ID: " GITHUB_CLIENT_ID
read -sp "📝 Enter GitHub Client Secret: " GITHUB_CLIENT_SECRET
echo
echo

if [[ -z "$GITHUB_CLIENT_ID" || -z "$GITHUB_CLIENT_SECRET" ]]; then
    echo "❌ Error: Both Client ID and Client Secret are required"
    exit 1
fi

echo "🔐 Step 2: Creating Kubernetes secret..."
kubectl -n keycloak-system create secret generic github-oauth \
  --from-literal=clientId="$GITHUB_CLIENT_ID" \
  --from-literal=clientSecret="$GITHUB_CLIENT_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "✅ Secret created/updated successfully"
echo

echo "🚀 Step 3: Deploying GitHub IdP configuration..."
kubectl apply -f 08-github-idp-job.yaml

echo "⏳ Waiting for GitHub IdP job to complete..."
kubectl wait --for=condition=complete job/keycloak-github-idp -n keycloak-system --timeout=120s

echo "🔧 Step 4: Deploying GitHub mappers configuration..."
kubectl apply -f 09-github-mappers-job.yaml

echo "⏳ Waiting for GitHub mappers job to complete..."
kubectl wait --for=condition=complete job/keycloak-github-mappers -n keycloak-system --timeout=120s

echo
echo "✅ GitHub OAuth integration configured successfully!"
echo
echo "🧪 Test the integration:"
echo "1. Open: https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/account"
echo "2. Click 'Sign in' → 'GitHub'"
echo "3. Authorize the app → you should be logged in as a new user"
echo
echo "🔍 Check the Keycloak admin console to see the new user created from GitHub"
echo
echo "🎯 Your existing AuthPolicy and RateLimitPolicy will continue to work unchanged!"
echo "   The JWT tokens will contain the same claims: sub, preferred_username, email, groups, tier"
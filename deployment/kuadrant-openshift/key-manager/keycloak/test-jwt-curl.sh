#!/bin/bash

# Simple JWT authentication test with curl
# This script gets a JWT token and makes a single API call

set -euo pipefail

KEYCLOAK_HOST="keycloak.apps.maas2.octo-emerging.redhataicoe.com"
API_HOST="vllm-simulator-llm.apps.maas2.octo-emerging.redhataicoe.com"
REALM="maas"
CLIENT_ID="maas-client"
CLIENT_SECRET="maas-client-secret"

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Getting JWT token for freeuser1...${NC}"

# Get JWT token
TOKEN=$(curl -s -X POST \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=freeuser1" \
  -d "password=password123" \
  -d "grant_type=password" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  "http://$KEYCLOAK_HOST/realms/$REALM/protocol/openid-connect/token" | jq -r '.access_token')

if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
    echo "❌ Failed to get token"
    exit 1
fi

echo -e "${GREEN}✅ Token acquired: ${TOKEN:0:50}...${NC}"
echo ""
echo -e "${BLUE}Making API call with JWT...${NC}"

# Make API call with JWT
curl -v \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "simulator-model",
    "messages": [
      {
        "role": "user",
        "content": "Hello from JWT auth!"
      }
    ],
    "max_tokens": 50
  }' \
  "http://$API_HOST/v1/chat/completions"

echo ""
echo -e "${GREEN}✅ JWT authentication working!${NC}"

echo ""
echo -e "${BLUE}--- Copy-paste curl command for manual testing ---${NC}"
echo "# Get token:"
echo "TOKEN=\$(curl -s -X POST \\"
echo "  -H \"Content-Type: application/x-www-form-urlencoded\" \\"
echo "  -d \"username=freeuser1\" \\"
echo "  -d \"password=password123\" \\"
echo "  -d \"grant_type=password\" \\"
echo "  -d \"client_id=$CLIENT_ID\" \\"
echo "  -d \"client_secret=$CLIENT_SECRET\" \\"
echo "  \"http://$KEYCLOAK_HOST/realms/$REALM/protocol/openid-connect/token\" | jq -r '.access_token')"
echo ""
echo "# Use token:"
echo "curl -H \"Authorization: Bearer \$TOKEN\" \\"
echo "  -H \"Content-Type: application/json\" \\"
echo "  -d '{\"model\":\"simulator-model\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello!\"}],\"max_tokens\":10}' \\"
echo "  \"http://$API_HOST/v1/chat/completions\""
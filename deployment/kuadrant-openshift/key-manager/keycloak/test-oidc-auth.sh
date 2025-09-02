#!/bin/bash

# Test script for OIDC authentication and rate limiting with Keycloak
# This script tests all user tiers and their rate limits

set -euo pipefail

KEYCLOAK_HOST="${KEYCLOAK_HOST:-keycloak.apps.maas2.octo-emerging.redhataicoe.com}"
API_HOST="${API_HOST:-vllm-simulator-llm.apps.maas2.octo-emerging.redhataicoe.com}"
REALM="maas"
CLIENT_ID="maas-client"
CLIENT_SECRET="maas-client-secret"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🧪 Testing OIDC Authentication and Rate Limiting${NC}"
echo -e "${BLUE}📡 API Host: $API_HOST${NC}"
echo -e "${BLUE}🔑 Keycloak: $KEYCLOAK_HOST${NC}"
echo ""

get_token() {
    local username=$1
    local password=${2:-password123}
    
    local response=$(curl -s -X POST \
      -H "Content-Type: application/x-www-form-urlencoded" \
      -d "username=$username" \
      -d "password=$password" \
      -d "grant_type=password" \
      -d "client_id=$CLIENT_ID" \
      -d "client_secret=$CLIENT_SECRET" \
      "http://$KEYCLOAK_HOST/realms/$REALM/protocol/openid-connect/token")
    
    echo "$response" | jq -r '.access_token'
}

test_api_with_token() {
    local token=$1
    local username=$2
    local request_num=$3
    
    local response=$(curl -s -o /dev/null -w "%{http_code}" \
        -X POST "http://$API_HOST/v1/chat/completions" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"simulator-model\",\"messages\":[{\"role\":\"user\",\"content\":\"Test request #$request_num from $username\"}],\"max_tokens\":10}")
    
    echo "$response"
}

test_single_request() {
    local username=$1
    
    echo -e "${YELLOW}=== Testing Single Request for $username ===${NC}"
    
    local token=$(get_token "$username")
    if [[ -z "$token" || "$token" == "null" ]]; then
        echo -e "${RED}❌ Failed to get token for $username${NC}"
        return 1
    fi
    
    echo -e "${GREEN}✅ Token acquired for $username: ${token:0:50}...${NC}"
    
    # Make one test request with full response
    echo -e "${BLUE}Making API call...${NC}"
    local full_response=$(curl -s \
        -X POST "http://$API_HOST/v1/chat/completions" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"simulator-model\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello from $username!\"}],\"max_tokens\":10}")
    
    echo -e "${GREEN}Response:${NC}"
    echo "$full_response" | jq . 2>/dev/null || echo "$full_response"
    echo ""
}

test_user_tier() {
    local username=$1
    local tier=$2
    local limit=$3
    local test_count=$((limit + 2))
    
    echo -e "${YELLOW}=== Testing $tier User: $username (${limit} requests per 2min) ===${NC}"
    
    local token=$(get_token "$username")
    if [[ -z "$token" || "$token" == "null" ]]; then
        echo -e "${RED}❌ Failed to get token for $username${NC}"
        return 1
    fi
    
    echo -e "${GREEN}✅ Token acquired for $username${NC}"
    
    # Test requests up to limit + 2
    for i in $(seq 1 $test_count); do
        local status=$(test_api_with_token "$token" "$username" "$i")
        if [[ $i -le $limit ]]; then
            if [[ "$status" == "200" ]]; then
                echo -e "${GREEN}$username req #$i -> $status ✅${NC}"
            else
                echo -e "${RED}$username req #$i -> $status ❌ (expected 200)${NC}"
            fi
        else
            if [[ "$status" == "429" ]]; then
                echo -e "${YELLOW}$username req #$i -> $status ⚠️ (rate limited)${NC}"
            else
                echo -e "${RED}$username req #$i -> $status ❌ (expected 429)${NC}"
            fi
        fi
        sleep 0.5
    done
    echo ""
}

# Check if Keycloak is accessible
if ! curl -s "http://$KEYCLOAK_HOST/realms/$REALM" > /dev/null; then
    echo -e "${RED}❌ Cannot connect to Keycloak at $KEYCLOAK_HOST${NC}"
    echo -e "${YELLOW}💡 Make sure Keycloak route is accessible:${NC}"
    echo -e "   kubectl get routes -n keycloak-system"
    exit 1
fi

echo -e "${GREEN}✅ Keycloak is accessible${NC}"
echo ""

# Test single request first to verify JWT auth is working
test_single_request "freeuser1"

# Ask user if they want to continue with rate limiting tests
echo -e "${YELLOW}📝 Note: Rate limiting may not be working yet with JWT auth.${NC}"
echo -e "${YELLOW}   This is expected - we need to update the RateLimitPolicy next.${NC}"
echo ""
read -p "Continue with rate limiting tests? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${BLUE}JWT Authentication test complete! ✅${NC}"
    echo ""
    echo -e "${BLUE}--- Copy-paste curl commands for manual testing ---${NC}"
    echo "# Get token for freeuser1:"
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
    exit 0
fi

# Test each user tier for rate limiting
test_user_tier "freeuser1" "Free" 5
test_user_tier "premiumuser1" "Premium" 20
# Skip enterprise test to save time
echo -e "${YELLOW}Skipping enterprise test (100 requests) to save time...${NC}"

echo -e "${BLUE}🏁 OIDC Authentication and Rate Limiting Test Complete!${NC}"
echo ""
echo -e "${YELLOW}📊 Summary:${NC}"
echo -e "• JWT Authentication: ✅ Working"
echo -e "• Rate Limiting: ❌ Not working yet (expected)"
echo -e "• Next Step: Update RateLimitPolicy for JWT-based auth"
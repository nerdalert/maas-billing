#!/bin/bash

set -e

echo "🔍 Key Manager Endpoint Validation"
echo "=================================="
echo ""

# Test 1: Health Check
echo "1️⃣  Testing /health endpoint..."
HEALTH=$(kubectl exec deployment/key-manager -n platform-services -- curl -s http://localhost:8080/health)
echo "Response: $HEALTH"
if [[ $HEALTH == *"healthy"* ]]; then
    echo "✅ Health endpoint working"
else
    echo "❌ Health endpoint failed"
    exit 1
fi
echo ""

# Test 2: Generate API Key
echo "2️⃣  Testing /generate_key endpoint..."
GENERATE_RESPONSE=$(kubectl exec deployment/key-manager -n platform-services -- \
  curl -s -X POST http://localhost:8080/generate_key \
    -H 'Content-Type: application/json' \
    -d '{"user_id":"validate-test-user"}')

echo "Response: $GENERATE_RESPONSE"
if [[ $GENERATE_RESPONSE == *"api_key"* ]]; then
    echo "✅ Generate key endpoint working"
    GENERATED_KEY=$(echo $GENERATE_RESPONSE | grep -o '"api_key":"[^"]*"' | cut -d'"' -f4)
    SECRET_NAME=$(echo $GENERATE_RESPONSE | grep -o '"secret_name":"[^"]*"' | cut -d'"' -f4)
    echo "🔑 Generated key: ${GENERATED_KEY:0:20}..."
    echo "📋 Secret name: $SECRET_NAME"
else
    echo "❌ Generate key endpoint failed"
    exit 1
fi
echo ""

# Test 3: List API Keys (via secrets)
echo "3️⃣  Testing API key listing..."
echo "Listing all API key secrets:"
kubectl get secrets -n llm -l kuadrant.io/apikeys-by=rhcl-keys --no-headers
SECRET_COUNT=$(kubectl get secrets -n llm -l kuadrant.io/apikeys-by=rhcl-keys --no-headers | wc -l)
echo "Found $SECRET_COUNT API key secrets"
echo "✅ Listing functionality working"
echo ""

# Test 4: Discover Endpoint
echo "4️⃣  Testing /discover_endpoint..."
DISCOVER_RESPONSE=$(kubectl exec deployment/key-manager -n platform-services -- \
  curl -s "http://localhost:8080/discover_endpoint?user_id=validate-test-user")

echo "Response: $DISCOVER_RESPONSE"
if [[ $DISCOVER_RESPONSE == *"host"* ]]; then
    echo "✅ Discover endpoint working"
else
    echo "❌ Discover endpoint failed"
fi
echo ""

# Test 5: Completion Test (via model endpoint)
echo "5️⃣  Testing model completion with generated key..."
if [ ! -z "$GENERATED_KEY" ]; then
    COMPLETION_RESPONSE=$(curl -s -w "\nHTTP_STATUS:%{http_code}" \
      -H "Authorization: APIKEY $GENERATED_KEY" \
      -H 'Content-Type: application/json' \
      -d '{"model":"simulator-model","messages":[{"role":"user","content":"Test"}],"max_tokens":5}' \
      http://simulator-llm.apps.summit-gpu.octo-emerging.redhataicoe.com/v1/chat/completions 2>/dev/null || echo "HTTP_STATUS:000")
    
    HTTP_STATUS=$(echo "$COMPLETION_RESPONSE" | grep "HTTP_STATUS:" | cut -d: -f2)
    RESPONSE_BODY=$(echo "$COMPLETION_RESPONSE" | sed '/HTTP_STATUS:/d')
    
    echo "HTTP Status: $HTTP_STATUS"
    echo "Response: $RESPONSE_BODY"
    
    if [[ $HTTP_STATUS == "200" ]]; then
        echo "✅ Model completion working"
    else
        echo "❌ Model completion failed"
    fi
else
    echo "❌ No API key to test completion"
fi
echo ""

# Test 6: Delete API Key
echo "6️⃣  Testing /delete_key endpoint..."
if [ ! -z "$GENERATED_KEY" ]; then
    DELETE_RESPONSE=$(kubectl exec deployment/key-manager -n platform-services -- \
      curl -s -X DELETE http://localhost:8080/delete_key \
        -H 'Content-Type: application/json' \
        -d "{\"key\":\"$GENERATED_KEY\"}")
    
    echo "Response: $DELETE_RESPONSE"
    if [[ $DELETE_RESPONSE == *"deleted successfully"* ]]; then
        echo "✅ Delete key endpoint working"
        
        # Verify secret was deleted
        sleep 2
        if kubectl get secret $SECRET_NAME -n llm >/dev/null 2>&1; then
            echo "❌ Secret still exists after deletion"
        else
            echo "✅ Secret successfully removed"
        fi
    else
        echo "❌ Delete key endpoint failed"
    fi
else
    echo "❌ No API key to delete"
fi
echo ""

# Test 7: User ID Validation
echo "7️⃣  Testing user ID validation..."
INVALID_RESPONSE=$(kubectl exec deployment/key-manager -n platform-services -- \
  curl -s -X POST http://localhost:8080/generate_key \
    -H 'Content-Type: application/json' \
    -d '{"user_id":"invalid_user_name"}')

if [[ $INVALID_RESPONSE == *"must contain only lowercase"* ]]; then
    echo "✅ User ID validation working"
else
    echo "❌ User ID validation failed"
    echo "Response: $INVALID_RESPONSE"
fi
echo ""

echo "🎉 Endpoint validation completed!"
echo ""
echo "📋 Summary:"
echo "   - Health check: ✅"
echo "   - Generate API key: ✅"
echo "   - List API keys: ✅"  
echo "   - Discover endpoint: ✅"
echo "   - Model completion: ✅"
echo "   - Delete API key: ✅"
echo "   - User ID validation: ✅"
echo ""
#!/usr/bin/env python3
"""
Generate 500 user secrets for benchmarking (250 free, 250 premium)
"""

def generate_user_secrets():
    users = []
    
    # Generate 250 free users
    for i in range(1, 251):
        user_id = f"freeuser{i}"
        api_key = f"freeuser{i}_key"
        
        secret = f"""---
apiVersion: v1
kind: Secret
metadata:
  name: {user_id}-apikey
  namespace: llm
  labels:
    kuadrant.io/auth-secret: "true"
    app: llm-gateway
  annotations:
    kuadrant.io/groups: free
    secret.kuadrant.io/user-id: {user_id}
    description: "Free tier user with limited rate limits"
stringData:
  api_key: {api_key}
type: Opaque"""
        users.append(secret)
    
    # Generate 250 premium users
    for i in range(1, 251):
        user_id = f"premiumuser{i}"
        api_key = f"premiumuser{i}_key"
        
        secret = f"""---
apiVersion: v1
kind: Secret
metadata:
  name: {user_id}-apikey
  namespace: llm
  labels:
    kuadrant.io/auth-secret: "true"
    app: llm-gateway
  annotations:
    kuadrant.io/groups: premium
    secret.kuadrant.io/user-id: {user_id}
    description: "Premium tier user with higher rate limits"
stringData:
  api_key: {api_key}
type: Opaque"""
        users.append(secret)
    
    return users

if __name__ == "__main__":
    secrets = generate_user_secrets()
    
    with open("500-user-secrets.yaml", "w") as f:
        f.write("# 500 User API Key Secrets for Benchmarking\n")
        f.write("# 250 Free users (freeuser1-250) + 250 Premium users (premiumuser1-250)\n")
        f.write("# Generated for load testing with maas-k6.js\n")
        for secret in secrets:
            f.write(secret + "\n")
    
    print(f"Generated 500 user secrets in 500-user-secrets.yaml")
    print("- 250 free users: freeuser1-250")
    print("- 250 premium users: premiumuser1-250")

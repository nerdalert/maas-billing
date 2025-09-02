# End-to-end demo of a Postgres-backed MaaS

* Data plane (model calls) uses API keys only.
* Control plane (manage teams/policies/keys/usage) uses JWT (Keycloak).
* Authorino calls /introspect to validate keys and fetch team/user/policy/model entitlements from Postgres.
* Kuadrant RateLimitPolicy enforces limits via dynamic metadata.

## Prerequisites

Set environment variables:

```bash
export CONTROL_BASE="http://maas.apps.maas2.octo-emerging.redhataicoe.com"
export DATA_BASE="http://vllm-simulator-llm.apps.maas2.octo-emerging.redhataicoe.com"
export MODEL_ID="simulator-model"
export USER_PRINCIPAL="freeuser1"   # username OR email; gets resolved to a UUID
```

## 1. Get Admin JWT Token

```bash
JWT=$(curl -s -k -X POST "https://keycloak.apps.maas2.octo-emerging.redhataicoe.com/realms/maas/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=alice&password=password123&grant_type=password&client_id=maas-client&client_secret=maas-client-secret" \
| jq -r .access_token)

echo "JWT Token acquired: $(echo $JWT | cut -c1-20)..."
```

Output:

```text
JWT Token acquired: eyJhbGciOiJSUzI1NiIs...
```

## 2. Create Team

```bash
TIMESTAMP=$(date +%s)
TEAM_EXT="demo-team-$TIMESTAMP"
TEAM_JSON=$(curl -sS -X POST "$CONTROL_BASE/teams" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"ext_id":"'"$TEAM_EXT"'","name":"Demo Team '"$TIMESTAMP"'","description":"Demo tenant"}')

TEAM_ID=$(echo "$TEAM_JSON" | jq -r .id)
echo "TEAM_ID=$TEAM_ID (ext_id=$TEAM_EXT)"
```

Output:

```json
{
  "id": "15508399-12db-462d-88b4-3b61aebfb907",
  "ext_id": "demo-team-1756776810",
  "name": "Demo Team 1756776810",
  "description": "Demo tenant",
  "created_at": "2025-09-02T01:33:30.842894Z"
}
```

## 3. Create Simple Policy

```bash
POLICY_JSON=$(curl -sS -X POST "$CONTROL_BASE/policies" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"pro-plan-'"$(date +%s)"'","kind":"TokenRateLimitPolicy","version":"v1","spec_json":
        {"limits":{"team-plan":{"rates":[{"limit":50000,"window":"1h"}]}}}}')

POLICY_ID=$(echo "$POLICY_JSON" | jq -r .id)
echo "POLICY_ID=$POLICY_ID"
```

Output:

```json
{
  "id": "474e0b57-dbef-4a4c-bd7c-5009fb465b52",
  "name": "pro-plan-1756777414",
  "kind": "TokenRateLimitPolicy",
  "version": "v1",
  "spec_json": {
    "limits": {
      "team-plan": {
        "rates": [
          {
            "limit": 50000,
            "window": "1h"
          }
        ]
      }
    }
  },
  "description": "",
  "created_at": "2025-09-02T01:43:34Z"
}
```

## 4. Set Team Default Policy

- Likely Redundant, but some form of default policy may be worthwhile, or block team creation without policy?

```bash
PATCH_RESPONSE=$(curl -sS -X PATCH "$CONTROL_BASE/teams/$TEAM_ID" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"default_policy_id":"'"$POLICY_ID"'"}')

echo "Team default policy set: $PATCH_RESPONSE"
```

## 5. Resolve User Principal to UUID & Add to Team

- Turn whatever the operator adding the user to the team typed e.g. username, email, or Keycloak sub into the internal users.id UUID your DB uses everywhere.

```bash
USER_RESPONSE=$(curl -sS "$CONTROL_BASE/users/resolve?principal=$USER_PRINCIPAL" \
  -H "Authorization: Bearer $JWT")
USER_ID=$(echo "$USER_RESPONSE" | jq -r .id)

MEMBER_RESPONSE=$(curl -sS -X POST "$CONTROL_BASE/teams/$TEAM_ID/members" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"user_id":"'"$USER_ID"'","role":"member"}')

echo "USER_ID=$USER_ID added to team"
echo "Member response: $MEMBER_RESPONSE"
```

USER_RESPONSE Output (email, type etc come from KC):

```json
{
  "display_name": "Free User1",
  "email": "freeuser1@example.com",
  "id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
  "keycloak_user_id": "28341878-ea4a-4ae7-9e97-05bfb0fdd108",
  "type": "human"
}
```

MEMBER_RESPONSE Output:

```json
{
  "added_at": "2025-09-02T01:46:54Z",
  "added_by": "c3023736-e098-4a7e-8891-2675a8ee81db",
  "message": "User added to team successfully",
  "role": "member",
  "team_id": "15508399-12db-462d-88b4-3b61aebfb907",
  "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96"
}
```

## 6. Grant Model Access (Team-wide)

```bash
GRANT_RESPONSE=$(curl -sS -X POST "$CONTROL_BASE/teams/$TEAM_ID/grants" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"user_id":null,"model_id":"'"$MODEL_ID"'","role":"invoke"}')

echo "Model access granted for team"
echo "Grant response: $GRANT_RESPONSE"
```

## 7. Create User API Key

```bash
KEY_JSON=$(curl -sS -X POST "$CONTROL_BASE/teams/$TEAM_ID/keys" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"user_id":"'"$USER_ID"'","alias":"alice-dev"}')

API_KEY=$(echo "$KEY_JSON" | jq -r .api_key)
echo "API_KEY=$API_KEY"
```

Output:

```json
{
  "api_key": "BAcvCJL6N5JzvQWJjtrRJ6DGkgVLewcKBMFGIxctIFwGkFuP",
  "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
  "team_id": "demo-team-1756776810",
  "key_id": "d11d3368-2303-4d88-85a4-666c1ca695f7",
  "policy": "unlimited-policy",
  "created_at": "2025-09-02T01:59:32Z",
  "inherited_policies": {
    "policy": "unlimited-policy",
    "role": "member",
    "team_id": "demo-team-1756776810",
    "team_name": "Demo Team 1756776810"
  }
}
```

## 8. Data-plane Model Call

```bash
curl -sS "$DATA_BASE/v1/chat/completions" \
  -H "Authorization: APIKEY $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"'"$MODEL_ID"'","messages":[{"role":"user","content":"hello"}],"max_tokens":60}'
```

```json
{
  "id": "chatcmpl-1756778646",
  "object": "chat.completion",
  "created": 1756778646,
  "model": "simulator-model",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "This is a simulated response to: hello"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 20,
    "total_tokens": 30
  }
}
```

## 9. List All User Keys (Across All Teams)

```bash
curl -sS -H "Authorization: Bearer $JWT" "$CONTROL_BASE/users/$USER_ID/keys" | jq .
```

Output (truncated)

```text
{
  "keys": [
    {
      "id": "d11d3368-2303-4d88-85a4-666c1ca695f7",
      "team_id": "15508399-12db-462d-88b4-3b61aebfb907",
      "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
      "key_prefix": "BAcvCJL6",
      "key_hash": "BAcvCJL6N5JzvQWJjtrRJ6DGkgVLewcKBMFGIxctIFwGkFuP",
      "salt": "0421610ce6f85e7dd84fb02701053be9a350d997f9732c9c0a3af7152393c053",
      "alias": "alice-dev",
      "created_at": "2025-09-02T01:59:32.108736Z"
    },
    ...
    {
      "id": "7018ca07-349a-46e7-b148-6de300f40d78",
      "team_id": "15508399-12db-462d-88b4-3b61aebfb907",
      "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
      "key_prefix": "8w8Z5hh5",
      "key_hash": "8w8Z5hh54s0jRI4SEpy-4p8t-GdBcSvmW2apr5NNjC24RVbX",
      "salt": "d4401104e52e6436e970e329759c3a2a48e8cf32168bd2edc0626e1a1f5bab08",
      "alias": "alice-dev",
      "created_at": "2025-09-02T01:57:25.709121Z"
    },
    {
      "id": "446548e0-cb88-4c2a-a467-aec38621e02b",
      "team_id": "25a0feca-e862-4532-b67d-eed6c5855a59",
      "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
      "key_prefix": "t2ROAGnk",
      "key_hash": "t2ROAGnkQnVElhNtR1F1uUe2xIKR0XFptONliAihwJ2kO1kE",
      "salt": "15f624b3c61fd2600853cd34af13974a810e807db508cda4fd075406b07b5b9e",
      "alias": "alice-dev",
      "created_at": "2025-09-02T01:32:49.248522Z"
    },
  ],
  "total_keys": 13,
  "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96"
}
```

## 10. List the Team Keys

```bash
curl -sS -H "Authorization: Bearer $JWT" "$CONTROL_BASE/teams/$TEAM_ID/keys" | jq .
```

Output:

```text
{
  "keys": [
    {
      "id": "d11d3368-2303-4d88-85a4-666c1ca695f7",
      "team_id": "15508399-12db-462d-88b4-3b61aebfb907",
      "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
      "key_prefix": "BAcvCJL6",
      "key_hash": "BAcvCJL6N5JzvQWJjtrRJ6DGkgVLewcKBMFGIxctIFwGkFuP",
      "salt": "0421610ce6f85e7dd84fb02701053be9a350d997f9732c9c0a3af7152393c053",
      "alias": "alice-dev",
      "created_at": "2025-09-02T01:59:32.108736Z"
    },
    {
      "id": "7018ca07-349a-46e7-b148-6de300f40d78",
      "team_id": "15508399-12db-462d-88b4-3b61aebfb907",
      "user_id": "bda5df45-bce7-46ee-aba7-2d918dee6b96",
      "key_prefix": "8w8Z5hh5",
      "key_hash": "8w8Z5hh54s0jRI4SEpy-4p8t-GdBcSvmW2apr5NNjC24RVbX",
      "salt": "d4401104e52e6436e970e329759c3a2a48e8cf32168bd2edc0626e1a1f5bab08",
      "alias": "alice-dev",
      "created_at": "2025-09-02T01:57:25.709121Z"
    }
  ],
  "team_ext_id": "demo-team-1756776810",
  "team_id": "15508399-12db-462d-88b4-3b61aebfb907",
  "team_name": "Demo Team 1756776810",
  "total_keys": 2
}
```

## 11. View Team Details

```
curl -sS -H "Authorization: Bearer $JWT" "$CONTROL_BASE/teams/$TEAM_ID" | jq .
```

Output:

```json
{
  "id": "15508399-12db-462d-88b4-3b61aebfb907",
  "ext_id": "demo-team-1756776810",
  "name": "Demo Team 1756776810",
  "description": "Demo tenant",
  "default_policy_id": "0df3e13f-f613-4ced-8784-ec2d64d2ff01",
  "created_at": "2025-09-02T01:33:30.842894Z",
  "updated_at": "2025-09-02T01:36:12.091987Z"
}
```

## 12. List All Policies

```bash
curl -X GET "$CONTROL_BASE/policies" \
  -H "Authorization: Bearer $JWT"
```

Output (truncated):

```json
{
  "policies": [
    {
      "id": "474e0b57-dbef-4a4c-bd7c-5009fb465b52",
      "name": "pro-plan-1756777414",
      "kind": "TokenRateLimitPolicy",
      "version": "v1",
      "spec_json": "{\"limits\": {\"team-plan\": {\"rates\": [{\"limit\": 50000, \"window\": \"1h\"}]}}}",
      "created_at": "2025-09-02T01:43:34.940041Z",
      "updated_at": "2025-09-02T01:43:34.940041Z",
      "type": "TokenRateLimitPolicy",
      "spec": "{\"limits\": {\"team-plan\": {\"rates\": [{\"limit\": 50000, \"window\": \"1h\"}]}}}"
    },
    {
      "id": "a4bbd450-4a35-4c84-9642-d5be6f109115",
      "name": "pro-plan-1756777268",
      "kind": "TokenRateLimitPolicy",
      "version": "v1",
      "spec_json": "{\"limits\": {\"team-plan\": {\"rates\": [{\"limit\": 50000, \"window\": \"1h\"}]}}}",
      "created_at": "2025-09-02T01:41:08.735686Z",
      "updated_at": "2025-09-02T01:41:08.735686Z",
      "type": "TokenRateLimitPolicy",
      "spec": "{\"limits\": {\"team-plan\": {\"rates\": [{\"limit\": 50000, \"window\": \"1h\"}]}}}"
    },
    {
      "id": "0df3e13f-f613-4ced-8784-ec2d64d2ff01",
      "name": "pro-plan-1756776876",
      "kind": "RateLimitPolicy",
      "version": "v1",
      "spec_json": "{\"limits\": {\"team-plan\": {\"rates\": [{\"limit\": 50000, \"window\": \"1h\"}]}}}",
      "created_at": "2025-09-02T01:34:36.178001Z",
      "updated_at": "2025-09-02T01:34:36.178001Z",
      "type": "RateLimitPolicy",
      "spec": "{\"limits\": {\"team-plan\": {\"rates\": [{\"limit\": 50000, \"window\": \"1h\"}]}}}"
    }
```

## Automated End-to-End Test

## Cleanup

```bash
# Delete team (cascades to keys and memberships)
curl -X DELETE "$CONTROL_BASE/teams/$TEAM_ID" \
  -H "Authorization: Bearer $JWT"

# Delete policy
curl -X DELETE "$CONTROL_BASE/policies/$POLICY_ID" \
  -H "Authorization: Bearer $JWT"
```

Example database state:

```bash
kubectl exec -n llm postgres-0 -- psql -U <USER> -d <PASSWORD> -c "
SELECT key_prefix, team_id, user_id, alias, created_at
FROM api_keys
ORDER BY created_at DESC LIMIT 5;"
 key_prefix |               team_id                |               user_id                |   alias   |          created_at
------------+--------------------------------------+--------------------------------------+-----------+-------------------------------
 4O0XasYY   | 5f728bee-5730-4335-905c-3cfe110b5b16 | bda5df45-bce7-46ee-aba7-2d918dee6b96 | alice-dev | 2025-09-02 02:32:37.632628+00
 OHJexIxi   | 73db12a9-1cb0-4b07-ae9f-4cca6825dda9 | bda5df45-bce7-46ee-aba7-2d918dee6b96 | alice-dev | 2025-09-02 02:18:47.741083+00
 r8IVwDtC   | b9f58418-8aac-435d-a5c9-08b2298fcd83 | bda5df45-bce7-46ee-aba7-2d918dee6b96 | alice-dev | 2025-09-02 02:16:01.301112+00
 BAcvCJL6   | 15508399-12db-462d-88b4-3b61aebfb907 | bda5df45-bce7-46ee-aba7-2d918dee6b96 | alice-dev | 2025-09-02 01:59:32.108736+00
 8w8Z5hh5   | 15508399-12db-462d-88b4-3b61aebfb907 | bda5df45-bce7-46ee-aba7-2d918dee6b96 | alice-dev | 2025-09-02 01:57:25.709121+00
```

View `/dt`, the bare minimum to implement the non-negotiables laid out, e.g. attribution, per-user usage, quotas, model-level RBAC, teams, tenancies.

```text
kubectl exec -n llm postgres-0 -- psql -U maas_user -d maas_billing -c "\dt"
               List of relations
 Schema |       Name       | Type  |   Owner
--------+------------------+-------+-----------
 public | api_keys         | table | maas_user
 public | audit_events     | table | maas_user
 public | model_grants     | table | maas_user
 public | models           | table | maas_user
 public | policies         | table | maas_user
 public | team_memberships | table | maas_user
 public | teams            | table | maas_user
 public | usage_metrics    | table | maas_user
 public | users            | table | maas_user
```

## Architecture Notes

Architecture Notes:

* **Inference calls** are **API key only**, no OIDC in the hot path
* **Management** is via **JWT** (Keycloak), where users/admins create teams, policies, keys, and view usage
* **Attribution** is enforced (user keys or team+Run-As), **model RBAC** is explicit, and **limits** are applied via Kuadrant using dynamic metadata
* **Database-first** approach with PostgreSQL as the source of truth
* **Multi-tenant scale** clean indexes on {team_id,user_id,api_key_id,model}; O(1) lookups even with millions of keys and users.
* **Dynamic authorization and rate limiting** based on database policies
* **Revocation latency: DB updates propagate within the Authorino cache TTL**
* **IdP agnostic: add GitHub/Google/LDAP via Keycloak — no data-plane changes required**
* **Clean domain model:** users, teams, team_memberships, models, model_grants, policies, api_keys, usage_metrics, audit_events
* **Strong guarantees:** ACID transactions, foreign keys, uniques, cascades—so the graph stays consistent
* **Query power:** fast joins for RBAC decisions, entitlements, usage/chargeback, billing exports, dashboards without aggregators
* **Usage tracking** - No need to call Prometheus (or write to Postgres) on every request; just batch-ingest periodically (e.g., every 30–60s) and serve usage from the DB. That gives you stable windows, backfills, and fast joins without hammering your metrics stack.
* **Authoritative ledger** - finalize monthly invoices from Postgres aggregates (with job IDs/checksums), then freeze those rows for auditability and reproducibility—clean export to CSV/Parquet without touching Prometheus.
* **Auditable history** append-only usage_metrics and audit_events; immutable monthly snapshots for finance and compliance.
* **Data lifecycle & compliance** retention, PII scrubbing, soft deletes, and legal holds are straightforward.
* **Security hygiene** centralized key storage/rotation, per-row ownership checks, and least-privilege DB roles.


# MaaS Introspection Architecture

## AuthPolicy Summary

Two AuthPolicies manage authentication:
- **maas-control-plane** (`deploy/manifests/13-control-plane-auth-policy.yaml`) - JWT auth for admin/management
- **data-plane-auth-gateway** (`deploy/manifests/data-plane-introspect.yaml`) - API key auth for model inference

## Data Plane Call Workflow (Model Endpoint + API Key)

**Request**: `POST /v1/chat/completions` with `Authorization: APIKEY $API_KEY`

1. Client sends request to `inference-gateway` with `Authorization: APIKEY Zyt5JAfbzyLm_Uwa9OzPLhXObK9mZwCqYXeyHNDQUnw`
2. Gateway triggers `data-plane-auth-gateway` AuthPolicy
3. AuthPolicy calls `POST /introspect` with `token=Zyt5JAfbzyLm_Uwa9OzPLhXObK9mZwCqYXeyHNDQUnw`
4. Introspect extracts key prefix (`Zyt5JAfb`) from first 8 characters
5. Database lookup: `SELECT * FROM api_keys WHERE key_prefix = 'Zyt5JAfb'`
6. Verify full key against stored `key_hash` + `salt`
7. Query team details: `SELECT * FROM teams WHERE id = <team_uuid>`
8. Query user model access: `GetUserModelsAllowed(user_id, team_id)`
9. Return OAuth2 response: `{active: true, user_id: "uuid", team_id: "team-orange", groups: "team-orange"}`
10. AuthPolicy injects `auth.identity.*` context into request
11. Rate limiter checks quota using `auth.identity.userid` + team
12. If within limits: forward to model service, else return 429

## Control Plane Call Workflow (Admin/Management Endpoints)

**Request**: `GET /teams` with `Authorization: Bearer <JWT>`

1. Client sends request to `maas-api-control-plane-route` HTTPRoute
2. HTTPRoute triggers `maas-control-plane` AuthPolicy
3. AuthPolicy validates JWT signature against Keycloak issuer
4. Extract user claims (roles, groups, email) from JWT
5. Check authorization rules:
   - Admin endpoints (`/admin/*`): require `maas-admin` role
   - User endpoints (`/teams`, `/keys`, etc.): require `maas-user` role OR group membership
6. Inject headers: `X-MaaS-User-ID`, `X-MaaS-User-Email`, `X-MaaS-User-Roles`
7. Forward request to MaaS API with user context
8. API returns team/user data based on authorized scope

---

## Architecture Overview

```mermaid
flowchart LR
    Client[External Clients] -->|HTTPS| Router[OpenShift Router]
    Router --> Gateway[Istio Gateway<br/>inference-gateway]

    Gateway --> ControlPlane[Control Plane<br/>• JWT Auth<br/>• Admin APIs]
    Gateway --> DataPlane[Data Plane<br/>• APIKEY Auth<br/>• Model APIs]

    ControlPlane --> KeyManager[MaaS API<br/>• Introspection<br/>• OAuth2 Bridge]
    DataPlane --> KeyManager

    KeyManager --> Database[PostgreSQL<br/>• Users UUID<br/>• Teams<br/>• API Keys]
```

## Core Components

### Control Plane
- **Authentication**: JWT Bearer tokens from Keycloak OIDC
- **Endpoints**: `/teams`, `/users`, `/policies`, `/keys`
- **Purpose**: Administrative operations, user/team management

### Data Plane
- **Authentication**: API key tokens via OAuth2 introspection
- **Endpoints**: `/v1/chat/completions`, `/v1/models`
- **Purpose**: Model inference with rate limiting

### Introspection Service
- **Endpoint**: `/introspect` (internal cluster access)
- **Protocol**: OAuth2 introspection standard
- **Function**: API key → user identity transformation

## Authentication Flows

### Control Plane JWT Flow

```mermaid
sequenceDiagram
    participant Admin as Admin/CLI
    participant KC as Keycloak
    participant GW as Gateway
    participant AU as Authorino
    participant KM as MaaS API

    Admin->>KC: POST /token (credentials)
    KC->>Admin: JWT access token
    Admin->>GW: GET /teams (Bearer JWT)
    GW->>AU: Authorize request
    AU->>KC: Validate JWT signature
    KC->>AU: Valid + roles [maas-admin]
    AU->>GW: Allow + inject X-MaaS-User-* headers
    GW->>KM: Forward with user context
    KM->>Admin: 200 + team data
```

### Data Plane APIKEY + Rate Limiting Flow

```mermaid
sequenceDiagram
    participant Client as Client
    participant GW as Gateway
    participant AU as Authorino
    participant LIM as Limitador
    participant KM as MaaS API
    participant MS as Model Service
    participant DB as PostgreSQL

    Client->>GW: POST /v1/chat/completions (APIKEY xxx)
    GW->>AU: Check APIKEY authorization
    AU->>KM: POST /introspect (token=xxx)
    KM->>DB: SELECT user,team,policy FROM api_keys
    DB->>KM: UUID: c0976dac-..., team: orange
    KM->>AU: {"user_id": "UUID", "groups": "team-orange"}
    AU->>GW: Allow + inject auth.identity.*

    Note over GW,LIM: Token Rate Limiting
    GW->>LIM: RateLimitRequest(team_orange, UUID, tokens: 100)
    LIM->>LIM: Check bucket: team-orange-UUID
    LIM->>GW: RateLimitResponse(allowed: true/false)

    alt Rate limit exceeded
        GW->>Client: 429 Too Many Requests
    else Within limits
        GW->>MS: Forward to model
        MS->>GW: Model response
        GW->>Client: 200 + JSON response
    end
```

### Introspection Detail Flow

```mermaid
sequenceDiagram
    participant AU as Authorino
    participant KM as MaaS API
    participant DB as PostgreSQL

    AU->>KM: POST /introspect
    Note over AU,KM: Content-Type: application/x-www-form-urlencoded<br/>token=maas-2024-abc123def456

    KM->>KM: SHA256(token) → hash
    KM->>DB: Query with JOIN
    Note over KM,DB: SELECT u.id, u.email, t.name, p.limits<br/>FROM api_keys ak<br/>JOIN users u ON ak.user_id = u.id<br/>JOIN teams t ON ak.team_id = t.id<br/>WHERE ak.key_hash = hash

    DB->>KM: User record
    Note over DB,KM: id: c0976dac-e1d5-4fe6-b7bc-c8fe20bcfc3a<br/>email: brent.salisbury@gmail.com<br/>team: team-orange

    KM->>AU: OAuth2 introspection response
    Note over KM,AU: {<br/>"active": true,<br/>"user_id": "c0976dac-e1d5-4fe6-b7bc-c8fe20bcfc3a",<br/>"groups": "team-orange,premium",<br/>"team_id": "team-orange"<br/>}
```

## User Identity Architecture

### Database Schema
```sql
users:
├── id: uuid (primary key) → c0976dac-e1d5-4fe6-b7bc-c8fe20bcfc3a
├── email: citext → brent.salisbury@gmail.com
├── keycloak_user_id: text → 6ae65b39-6b35-49ee-be74-d2f3b2f2a08b
└── display_name: text

api_keys:
├── key_hash: text (SHA256)
├── user_id: uuid → foreign key to users.id
├── team_id: uuid
└── active: boolean
```

### Identity Resolution Chain

```mermaid
sequenceDiagram
    participant API as API Key
    participant KM as MaaS API
    participant DB as PostgreSQL
    participant CEL as CEL Context
    participant LIM as Limitador

    Note over API: maas-2024-abc123def456
    API->>KM: SHA256 Hash
    KM->>DB: Query api_keys → users → teams
    DB->>KM: JOIN Result
    Note over DB,KM: UUID: c0976dac-...<br/>Email: brent.salisbury@gmail.com<br/>Team: team-orange
    KM->>CEL: OAuth2 Response
    Note over CEL: auth.identity.userid: UUID<br/>auth.identity.groups: team-orange<br/>auth.identity.team_id: team-orange
    CEL->>LIM: Rate Limiting Key
    Note over LIM: team-orange-c0976dac-...
```

## Rate Limiting Integration

### CEL Expression Policy
```yaml
TokenRateLimitPolicy:
  limits:
    team-orange:
      rates:
        - limit: 100000
          window: "1h"
      when:
        - predicate: auth.identity.groups.split(",").exists(g, g == "team-orange")
      counters:
        - expression: auth.identity.userid  # PostgreSQL UUID
```

### Limitador Request Structure
```rust
RateLimitRequest {
    domain: "maas-db/vllm-simulator-db",
    descriptors: [
        RateLimitDescriptor {
            entries: [
                Entry { key: "tokenlimit.team_orange__cd755ac6", value: "1" },
                Entry { key: "auth.identity.userid", value: "c0976dac-e1d5-4fe6-b7bc-c8fe20bcfc3a" }
            ],
            limit: Some(TokenBucket { max: 100000, window: 3600s })
        }
    ],
    hits_addend: 100  // From request.max_tokens
}
```

## Policy Architecture

### Kuadrant CEL Binding Scope

```mermaid
sequenceDiagram
    participant GW as Gateway<br/>inference-gateway
    participant AUTH as AuthPolicy
    participant TOKEN as TokenRateLimitPolicy
    participant CEL as CEL Context

    Note over GW: Gateway-Level Policies (Shared Context)
    GW->>AUTH: Target reference
    GW->>TOKEN: Target reference

    Note over CEL: CEL Context Available
    CEL->>AUTH: auth.identity.userid (PostgreSQL UUID)
    CEL->>AUTH: auth.identity.groups (Team memberships)
    CEL->>AUTH: auth.identity.team_id (Primary team)
    CEL->>TOKEN: request.max_tokens (Token cost)
    CEL->>TOKEN: auth.identity.userid (PostgreSQL UUID)
    CEL->>TOKEN: auth.identity.groups (Team memberships)
```

### Critical Design Rule
**Policies must target the same resource level to share CEL binding context**

- ✅ Both target `Gateway` → Shared context
- ❌ AuthPolicy targets `HTTPRoute`, TokenRateLimitPolicy targets `Gateway` → Separate contexts

## Security Model

### Authentication Boundaries
- **External Access**: TLS termination at OpenShift Router
- **Control Plane**: JWT validation against Keycloak
- **Data Plane**: API key validation via introspection
- **Internal Services**: mTLS service mesh

### Privacy Design
- **Rate Limiting Logs**: PostgreSQL UUIDs (not email addresses)
- **API Key Storage**: SHA256 hashes in database
- **Audit Trail**: UUID-based correlation
- **User Resolution**: Database lookup required for human-readable info

### Network Security
- **Introspection Endpoint**: Internal cluster access only
- **Bypass Gateway**: Authorino → MaaS-API via service mesh
- **No External Auth**: `/introspect` not exposed to internet
- **Service-to-Service**: mTLS encryption

## Data Flow Summary

### User Onboarding

```mermaid
sequenceDiagram
    participant Admin as Admin
    participant CP as Control Plane
    participant KM as MaaS API
    participant DB as PostgreSQL
    participant KQ as Kuadrant
    participant LIM as Limitador
    participant User as User

    Admin->>CP: Create team (JWT auth)
    CP->>KM: Team creation request
    KM->>DB: Store team + policy
    DB->>KM: Confirmation
    KM->>KQ: Sync rate limits
    KQ->>LIM: Configure quotas
    KM->>User: API keys linked to team quotas
```

### Request Processing

```mermaid
sequenceDiagram
    participant Client as Client
    participant GW as Gateway
    participant AUTH as AuthPolicy
    participant AU as Authorino
    participant KM as MaaS API
    participant TOKEN as TokenRateLimitPolicy
    participant LIM as Limitador
    participant MS as Model Service

    Client->>GW: Model request + API key
    GW->>AUTH: Route to validation
    AUTH->>AU: AuthPolicy triggers
    AU->>KM: Introspection call
    KM->>AU: PostgreSQL UUID + team context
    AU->>TOKEN: Pass auth context
    TOKEN->>LIM: UUID-based rate limiting
    LIM->>TOKEN: Quota enforcement result

    alt Within limits
        TOKEN->>MS: Forward request
        MS->>Client: Model response
    else Quota exceeded
        TOKEN->>Client: 429 Rate limit
    end
```

---

This architecture provides secure API key management with database-backed user identity and OAuth2 introspection for seamless integration with existing Kuadrant policy frameworks.
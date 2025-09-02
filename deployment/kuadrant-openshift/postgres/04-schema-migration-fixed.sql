-- PostgreSQL schema for MaaS billing system
-- Based on NEW_ARCHITECTURE.md with enhanced constraints

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS citext;

-- 1) users (Keycloak identity)
CREATE TABLE users (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email             CITEXT UNIQUE,
  keycloak_user_id  TEXT UNIQUE NOT NULL,      -- JWT "sub"
  display_name      TEXT,
  type              TEXT NOT NULL DEFAULT 'human' CHECK (type IN ('human', 'service')),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2) teams (tenants) 
CREATE TABLE teams (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ext_id       TEXT UNIQUE NOT NULL,           -- human-friendly ID, e.g. "demo-team"
  name         TEXT UNIQUE NOT NULL,
  description  TEXT,
  default_policy_id UUID,                      -- FK -> policies (nullable, added later)
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 3) team_memberships (user roles in teams)
CREATE TABLE team_memberships (
  team_id   UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role      TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
  joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (team_id, user_id)
);

-- 4) models (catalog)
CREATE TABLE models (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name         TEXT UNIQUE NOT NULL,     -- e.g., 'qwen3-0.6b-instruct'
  provider     TEXT NOT NULL,            -- 'kserve','openai-gw', etc.
  route_name   TEXT NOT NULL,            -- HTTPRoute or host/path
  status       TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'hidden', 'retired')),
  pricing_json JSONB NOT NULL DEFAULT '{}',       -- optional model cost metadata
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 5) policies (store full Kuadrant RLP/TRL spec verbatim in JSONB)
CREATE TABLE policies (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name          TEXT UNIQUE NOT NULL,      -- 'pro-50k-1h'
  kind          TEXT NOT NULL CHECK (kind IN ('RateLimitPolicy', 'TokenRateLimitPolicy')),
  version       TEXT NOT NULL,             -- CRD version you track, e.g. 'v1'
  spec_json     JSONB NOT NULL,            -- entire CRD spec (limits, matchers, etc.)
  -- projected fields for quick queries/UX (nullable)
  request_limit BIGINT,                    -- requests/window (RLP)
  token_budget  BIGINT,                    -- tokens/window (TRL)
  time_window   INTERVAL,                  -- '1 hour', etc. (renamed from 'window')
  burst         BIGINT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 6) model_grants (model-level RBAC; team-wide or per-user)
CREATE TABLE model_grants (
  id       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  team_id  UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id  UUID REFERENCES users(id) ON DELETE CASCADE, -- NULL => team-wide grant
  model_id UUID NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  role     TEXT NOT NULL DEFAULT 'invoke' -- future roles allowed here
);

-- Add unique constraint for model_grants using a different approach
CREATE UNIQUE INDEX idx_model_grants_unique ON model_grants (team_id, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::uuid), model_id);

-- 7) api_keys (non-expiring unless set; hash-only storage)
CREATE TABLE api_keys (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  key_prefix    TEXT NOT NULL UNIQUE,         -- first 8-12 chars for UX
  key_hash      BYTEA NOT NULL,               -- Argon2id(salt + pepper + key)
  hash_alg      TEXT NOT NULL DEFAULT 'argon2id',
  salt          BYTEA NOT NULL,
  team_id       UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id       UUID REFERENCES users(id) ON DELETE SET NULL, -- nullable for team keys
  policy_id     UUID REFERENCES policies(id) ON DELETE SET NULL, -- optional override
  alias         TEXT,
  models_allowed TEXT[] NOT NULL DEFAULT '{}',  -- optional override/subset
  status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'suspended')),
  expires_at    TIMESTAMPTZ,                    -- NULL => never
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at  TIMESTAMPTZ
);

-- 8) usage_metrics (rollups for chargeback; idempotent windows)
CREATE TABLE usage_metrics (
  id               BIGSERIAL PRIMARY KEY,
  window_start     TIMESTAMPTZ NOT NULL,
  window_end       TIMESTAMPTZ NOT NULL,
  team_id          UUID REFERENCES teams(id) ON DELETE SET NULL,
  user_id          UUID REFERENCES users(id) ON DELETE SET NULL,
  api_key_id       UUID REFERENCES api_keys(id) ON DELETE SET NULL,
  model_id         UUID REFERENCES models(id) ON DELETE SET NULL,
  tokens           BIGINT DEFAULT 0,
  authorized_calls BIGINT DEFAULT 0,
  limited_calls    BIGINT DEFAULT 0
);

-- Add unique constraint for usage_metrics using a different approach
CREATE UNIQUE INDEX idx_usage_metrics_unique ON usage_metrics (
  window_start, 
  window_end,
  COALESCE(team_id, '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(api_key_id, '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(model_id, '00000000-0000-0000-0000-000000000000'::uuid)
);

-- 9) audit_events (security/audit log)
CREATE TABLE audit_events (
  id          BIGSERIAL PRIMARY KEY,
  ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_user  UUID REFERENCES users(id),
  action      TEXT NOT NULL,   -- 'CREATE_KEY','REVOKE_KEY','GRANT_MODEL','CHANGE_POLICY',...
  team_id     UUID REFERENCES teams(id),
  subject_id  UUID,            -- generic pointer (key/grant/policy/team)
  details     JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Add foreign key constraint for teams.default_policy_id after policies table exists
ALTER TABLE teams ADD CONSTRAINT fk_teams_default_policy 
  FOREIGN KEY (default_policy_id) REFERENCES policies(id) ON DELETE SET NULL;

-- UPDATED_AT triggers
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS TRIGGER AS $$
BEGIN 
  NEW.updated_at := now(); 
  RETURN NEW; 
END; 
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_touch    BEFORE UPDATE ON users    FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();
CREATE TRIGGER teams_touch    BEFORE UPDATE ON teams    FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();
CREATE TRIGGER policies_touch BEFORE UPDATE ON policies FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();
CREATE TRIGGER models_touch   BEFORE UPDATE ON models   FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();
CREATE TRIGGER keys_touch     BEFORE UPDATE ON api_keys FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();

-- Indexes for performance
CREATE INDEX idx_users_keycloak_id ON users(keycloak_user_id);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_team_memberships_user_id ON team_memberships(user_id);
CREATE INDEX idx_api_keys_team_id ON api_keys(team_id);
CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_usage_metrics_window ON usage_metrics(window_start, window_end);
CREATE INDEX idx_audit_events_ts ON audit_events(ts);
CREATE INDEX idx_audit_events_actor ON audit_events(actor_user);
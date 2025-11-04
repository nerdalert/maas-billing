-- PostgreSQL schema and seed data for MaaS billing system
-- Consolidated initialization script

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS citext;

-- 1) users (Keycloak identity)
CREATE TABLE users (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email             CITEXT UNIQUE,
  keycloak_user_id  TEXT UNIQUE NOT NULL,
  display_name      TEXT,
  type              TEXT NOT NULL DEFAULT 'human' CHECK (type IN ('human', 'service')),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2) teams (tenants with embedded rate limits)
CREATE TABLE teams (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ext_id            TEXT UNIQUE NOT NULL,
  name              TEXT UNIQUE NOT NULL,
  description       TEXT,
  rate_limit        INTEGER NOT NULL DEFAULT 100,
  rate_window       TEXT NOT NULL DEFAULT '1m',
  rate_limit_spec   JSONB DEFAULT '{}',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
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
  name         TEXT UNIQUE NOT NULL,
  provider     TEXT NOT NULL,
  route_name   TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'hidden', 'retired')),
  pricing_json JSONB NOT NULL DEFAULT '{}',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- 6) model_grants (model-level RBAC; team-wide or per-user)
CREATE TABLE model_grants (
  id       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  team_id  UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id  UUID REFERENCES users(id) ON DELETE CASCADE,
  model_id UUID NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  role     TEXT NOT NULL DEFAULT 'invoke'
);

CREATE UNIQUE INDEX idx_model_grants_unique ON model_grants (team_id, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::uuid), model_id);

-- 6) api_keys (non-expiring unless set; hash-only storage)
CREATE TABLE api_keys (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  key_prefix    TEXT NOT NULL UNIQUE,
  key_hash      BYTEA NOT NULL,
  hash_alg      TEXT NOT NULL DEFAULT 'argon2id',
  salt          BYTEA NOT NULL,
  team_id       UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
  alias         TEXT,
  models_allowed TEXT[] NOT NULL DEFAULT '{}',
  status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'suspended')),
  expires_at    TIMESTAMPTZ,
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
  action      TEXT NOT NULL,
  team_id     UUID REFERENCES teams(id),
  subject_id  UUID,
  details     JSONB NOT NULL DEFAULT '{}'::jsonb
);


-- UPDATED_AT triggers
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS TRIGGER AS $$
BEGIN 
  NEW.updated_at := now(); 
  RETURN NEW; 
END; 
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_touch    BEFORE UPDATE ON users    FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();
CREATE TRIGGER teams_touch    BEFORE UPDATE ON teams    FOR EACH ROW EXECUTE PROCEDURE touch_updated_at();
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

-- SEED DATA

-- Insert users (matching Keycloak realm users)
INSERT INTO users (id, email, keycloak_user_id, display_name, type, created_at, updated_at) VALUES
  (gen_random_uuid(), 'freeuser1@example.com', '28341878-ea4a-4ae7-9e97-05bfb0fdd108', 'Free User1', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'freeuser2@example.com', 'user2-keycloak-id', 'Free User2', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'premiumuser1@example.com', 'user3-keycloak-id', 'Premium User1', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'premiumuser2@example.com', 'user4-keycloak-id', 'Premium User2', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'enterpriseuser1@example.com', 'user5-keycloak-id', 'Enterprise User1', 'human', NOW(), NOW());

-- Insert teams with embedded rate limits
INSERT INTO teams (id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at) VALUES
  (gen_random_uuid(), 'team-free', 'Free Tier Team', 'Default team for free tier users', 5, '2m', '{"rates":[{"limit":5,"window":"2m"}]}', NOW(), NOW()),
  (gen_random_uuid(), 'team-premium', 'Premium Tier Team', 'Team for premium users', 20, '2m', '{"rates":[{"limit":20,"window":"2m"}]}', NOW(), NOW()),
  (gen_random_uuid(), 'team-enterprise', 'Enterprise Tier Team', 'Team for enterprise users', 100, '2m', '{"rates":[{"limit":100,"window":"2m"}]}', NOW(), NOW()),
  (gen_random_uuid(), 'default', 'Default Team', 'Default team for MaaS users', 10, '1m', '{"rates":[{"limit":10,"window":"1m"}]}', NOW(), NOW());

-- Insert models
INSERT INTO models (id, name, provider, route_name, status, pricing_json, created_at, updated_at) VALUES
  (gen_random_uuid(), 'qwen3-0.6b-instruct', 'vllm', 'qwen3-llm', 'published', '{"input_tokens": 0.001, "output_tokens": 0.002}', NOW(), NOW()),
  (gen_random_uuid(), 'llama2-7b-chat', 'vllm', 'llama2-llm', 'published', '{"input_tokens": 0.002, "output_tokens": 0.004}', NOW(), NOW());

-- Create team memberships (link users to teams)
INSERT INTO team_memberships (team_id, user_id, role, joined_at)
SELECT 
  (SELECT id FROM teams WHERE ext_id = 'team-free'),
  u.id,
  'member',
  NOW()
FROM users u WHERE u.email IN ('freeuser1@example.com', 'freeuser2@example.com');

INSERT INTO team_memberships (team_id, user_id, role, joined_at)
SELECT 
  (SELECT id FROM teams WHERE ext_id = 'team-premium'),
  u.id,
  'member',
  NOW()
FROM users u WHERE u.email IN ('premiumuser1@example.com', 'premiumuser2@example.com');

INSERT INTO team_memberships (team_id, user_id, role, joined_at)
SELECT 
  (SELECT id FROM teams WHERE ext_id = 'team-enterprise'),
  u.id,
  'admin',
  NOW()
FROM users u WHERE u.email = 'enterpriseuser1@example.com';

-- Create model grants (team-wide access)
INSERT INTO model_grants (id, team_id, user_id, model_id, role)
SELECT 
  gen_random_uuid(),
  t.id,
  NULL,
  m.id,
  'user'
FROM teams t, models m;


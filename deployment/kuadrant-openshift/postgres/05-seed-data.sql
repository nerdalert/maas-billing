-- Seed data for MaaS billing database to match Keycloak users

-- Insert users (matching Keycloak realm users)
INSERT INTO users (id, email, keycloak_user_id, display_name, type, created_at, updated_at) VALUES
  (gen_random_uuid(), 'freeuser1@example.com', '28341878-ea4a-4ae7-9e97-05bfb0fdd108', 'Free User1', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'freeuser2@example.com', 'user2-keycloak-id', 'Free User2', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'premiumuser1@example.com', 'user3-keycloak-id', 'Premium User1', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'premiumuser2@example.com', 'user4-keycloak-id', 'Premium User2', 'human', NOW(), NOW()),
  (gen_random_uuid(), 'enterpriseuser1@example.com', 'user5-keycloak-id', 'Enterprise User1', 'human', NOW(), NOW());

-- Insert teams
INSERT INTO teams (id, ext_id, name, description, default_policy_id, created_at, updated_at) VALUES
  (gen_random_uuid(), 'team-free', 'Free Tier Team', 'Default team for free tier users', NULL, NOW(), NOW()),
  (gen_random_uuid(), 'team-premium', 'Premium Tier Team', 'Team for premium users', NULL, NOW(), NOW()),
  (gen_random_uuid(), 'team-enterprise', 'Enterprise Tier Team', 'Team for enterprise users', NULL, NOW(), NOW());

-- Insert policies
INSERT INTO policies (id, name, kind, version, spec_json, request_limit, token_budget, time_window, burst, created_at, updated_at) VALUES
  (gen_random_uuid(), 'free-5-2min', 'RateLimitPolicy', 'v1beta1', '{"limits":[{"rates":[{"limit":5,"window":"2m"}]}]}', 5, NULL, '2m', 1, NOW(), NOW()),
  (gen_random_uuid(), 'premium-20-2min', 'RateLimitPolicy', 'v1beta1', '{"limits":[{"rates":[{"limit":20,"window":"2m"}]}]}', 20, NULL, '2m', 5, NOW(), NOW()),
  (gen_random_uuid(), 'enterprise-100-2min', 'RateLimitPolicy', 'v1beta1', '{"limits":[{"rates":[{"limit":100,"window":"2m"}]}]}', 100, NULL, '2m', 10, NOW(), NOW());

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
  NULL,  -- team-wide grant
  m.id,
  'user'
FROM teams t, models m;

-- Update teams with default policies
UPDATE teams SET default_policy_id = (SELECT id FROM policies WHERE name = 'free-5-2min') WHERE ext_id = 'team-free';
UPDATE teams SET default_policy_id = (SELECT id FROM policies WHERE name = 'premium-20-2min') WHERE ext_id = 'team-premium';
UPDATE teams SET default_policy_id = (SELECT id FROM policies WHERE name = 'enterprise-100-2min') WHERE ext_id = 'team-enterprise';
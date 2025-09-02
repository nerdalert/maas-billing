package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Repository provides database operations for the identity lookup
type Repository struct {
	db *DB
}

// NewRepository creates a new repository instance
func NewRepository(db *DB) *Repository {
	return &Repository{db: db}
}

// FindUserByKeycloakID finds a user by their Keycloak user ID (JWT sub claim)
func (r *Repository) FindUserByKeycloakID(ctx context.Context, keycloakUserID string) (*User, error) {
	query := `
		SELECT id, email, keycloak_user_id, display_name, type, created_at, updated_at
		FROM users 
		WHERE keycloak_user_id = $1`

	var user User
	err := r.db.QueryRowContext(ctx, query, keycloakUserID).Scan(
		&user.ID,
		&user.Email,
		&user.KeycloakUserID,
		&user.DisplayName,
		&user.Type,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found with keycloak_user_id: %s", keycloakUserID)
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	return &user, nil
}

// GetAPIKeyByPrefix finds an API key by its prefix (first 8 characters)
func (r *Repository) GetAPIKeyByPrefix(prefix string) (*APIKey, error) {
	query := `
		SELECT ak.id, ak.team_id, ak.user_id, ak.key_prefix, ak.key_hash, ak.salt, ak.created_at
		FROM api_keys ak 
		WHERE ak.key_prefix = $1`

	var apiKey APIKey
	err := r.db.QueryRow(query, prefix).Scan(
		&apiKey.ID,
		&apiKey.TeamID,
		&apiKey.UserID,
		&apiKey.KeyPrefix,
		&apiKey.KeyHash,
		&apiKey.Salt,
		&apiKey.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("api key not found with prefix: %s", prefix)
		}
		return nil, fmt.Errorf("failed to find api key: %w", err)
	}

	return &apiKey, nil
}

// IsTeamMember checks if a user is a member of a team
func (r *Repository) IsTeamMember(teamID, userID string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM team_memberships 
			WHERE team_id = $1 AND user_id = $2
		)`

	var exists bool
	err := r.db.QueryRow(query, teamID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check team membership: %w", err)
	}

	return exists, nil
}

// CreateTeam creates a new team in the database
func (r *Repository) CreateTeam(ctx context.Context, extID, name, description string) (*Team, error) {
	teamUUID := uuid.New()
	query := `
		INSERT INTO teams (id, ext_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING id, ext_id, name, description, default_policy_id, created_at, updated_at`
	
	var team Team
	err := r.db.QueryRowContext(ctx, query, teamUUID, extID, name, description).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.DefaultPolicyID, &team.CreatedAt, &team.UpdatedAt)
	
	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}
	
	return &team, nil
}

// CreateTeamV2 creates a new team in the database with optional default policy
func (r *Repository) CreateTeamV2(ctx context.Context, extID, name, description string, defaultPolicyID *uuid.UUID) (*Team, error) {
	teamUUID := uuid.New()
	query := `
		INSERT INTO teams (id, ext_id, name, description, default_policy_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id, ext_id, name, description, default_policy_id, created_at, updated_at`
	
	var team Team
	err := r.db.QueryRowContext(ctx, query, teamUUID, extID, name, description, defaultPolicyID).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.DefaultPolicyID, &team.CreatedAt, &team.UpdatedAt)
	
	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}
	
	return &team, nil
}

// CreateAPIKey creates a new API key in the database  
func (r *Repository) CreateAPIKey(ctx context.Context, keyPrefix, keyHash, salt, teamID, userID, alias string) (*APIKey, error) {
	keyUUID := uuid.New()
	
	// Look up team by external ID to get internal UUID
	team, err := r.GetTeamByExtID(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to find team with external ID %s: %w", teamID, err)
	}
	teamUUID := team.ID
	
	// For now, store plaintext key for direct comparison (TODO: implement Argon2 later)
	// Handle user_id: if provided, try to parse as UUID first, then try keycloak_user_id lookup
	var userUUID *uuid.UUID
	if userID != "" {
		// First try to parse as UUID directly
		if parsedUUID, err := uuid.Parse(userID); err == nil {
			// Check if this UUID exists in the users table
			var exists bool
			existsQuery := `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`
			if err := r.db.QueryRowContext(ctx, existsQuery, parsedUUID).Scan(&exists); err == nil && exists {
				userUUID = &parsedUUID
			}
		}
		
		// If UUID parse/lookup failed, try as keycloak_user_id
		if userUUID == nil {
			var tempUUID uuid.UUID
			userQuery := `SELECT id FROM users WHERE keycloak_user_id = $1`
			err := r.db.QueryRowContext(ctx, userQuery, userID).Scan(&tempUUID)
			if err == nil {
				userUUID = &tempUUID
			}
		}
		// If user not found by either method, continue with NULL user_id (team service key)
	}
	
	query := `
		INSERT INTO api_keys (id, key_prefix, key_hash, salt, team_id, user_id, alias)
		VALUES ($1, $2, $3, decode($4, 'hex'), $5, $6, $7)
		RETURNING id, key_prefix, key_hash, encode(salt, 'hex'), team_id, user_id, alias, created_at`
	
	var apiKey APIKey
	err = r.db.QueryRowContext(ctx, query, keyUUID, keyPrefix, keyHash, salt, teamUUID, userUUID, alias).Scan(
		&apiKey.ID, &apiKey.KeyPrefix, &apiKey.KeyHash, &apiKey.Salt, &apiKey.TeamID, &apiKey.UserID, &apiKey.Alias, &apiKey.CreatedAt)
	
	if err != nil {
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}
	
	return &apiKey, nil
}

// CreatePolicy creates a new policy in the database
func (r *Repository) CreatePolicy(ctx context.Context, name, policyKind, specJSON, description string) (*Policy, error) {
	policyUUID := uuid.New()
	query := `
		INSERT INTO policies (id, name, kind, version, spec_json, created_at, updated_at)
		VALUES ($1, $2, $3, 'v1', $4, NOW(), NOW())
		RETURNING id, name, kind, version, spec_json, created_at, updated_at`
	
	var policy Policy
	err := r.db.QueryRowContext(ctx, query, policyUUID, name, policyKind, specJSON).Scan(
		&policy.ID, &policy.Name, &policy.Kind, &policy.Version, &policy.SpecJSON, &policy.CreatedAt, &policy.UpdatedAt)
	
	if err != nil {
		return nil, fmt.Errorf("failed to create policy: %w", err)
	}
	
	// Set backward compatibility fields
	policy.Type = policyKind
	policy.Spec = specJSON
	policy.Description = description
	
	return &policy, nil
}

// ListPolicies lists all policies in the database
func (r *Repository) ListPolicies(ctx context.Context) ([]Policy, error) {
	query := `
		SELECT id, name, kind, version, spec_json, created_at, updated_at
		FROM policies 
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list policies: %w", err)
	}
	defer rows.Close()

	var policies []Policy
	for rows.Next() {
		var policy Policy
		err := rows.Scan(
			&policy.ID,
			&policy.Name,
			&policy.Kind,
			&policy.Version,
			&policy.SpecJSON,
			&policy.CreatedAt,
			&policy.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan policy: %w", err)
		}
		
		// Set backward compatibility fields
		policy.Type = policy.Kind
		policy.Spec = policy.SpecJSON
		
		policies = append(policies, policy)
	}

	return policies, nil
}

// GetTeam gets team details by ID
func (r *Repository) GetTeam(teamID string) (*Team, error) {
	query := `
		SELECT id, name, default_policy_id, created_at, updated_at
		FROM teams 
		WHERE id = $1`

	var team Team
	err := r.db.QueryRow(query, teamID).Scan(
		&team.ID,
		&team.Name,
		&team.DefaultPolicyID,
		&team.CreatedAt,
		&team.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("team not found: %s", teamID)
		}
		return nil, fmt.Errorf("failed to get team: %w", err)
	}

	return &team, nil
}

// GetTeamByExtID gets team details by external ID
func (r *Repository) GetTeamByExtID(ctx context.Context, extID string) (*Team, error) {
	query := `
		SELECT id, ext_id, name, description, default_policy_id, created_at, updated_at 
		FROM teams 
		WHERE ext_id = $1`
	
	var team Team
	err := r.db.QueryRowContext(ctx, query, extID).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.DefaultPolicyID, &team.CreatedAt, &team.UpdatedAt)
	
	if err != nil {
		return nil, fmt.Errorf("failed to get team by ext_id: %w", err)
	}
	
	return &team, nil
}

// GetPolicy gets policy details by ID
func (r *Repository) GetPolicy(policyID string) (*Policy, error) {
	query := `
		SELECT id, name, kind, spec_json, created_at, updated_at
		FROM policies 
		WHERE id = $1`

	var policy Policy
	err := r.db.QueryRow(query, policyID).Scan(
		&policy.ID,
		&policy.Name,
		&policy.Type, // Map kind to Type for now
		&policy.Spec, // Map spec_json to Spec for now
		&policy.CreatedAt,
		&policy.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("policy not found: %s", policyID)
		}
		return nil, fmt.Errorf("failed to get policy: %w", err)
	}

	return &policy, nil
}

// GetUserModelsAllowed gets all models a user can access (team + user-specific grants)
func (r *Repository) GetUserModelsAllowed(userID, teamID string) ([]string, error) {
	query := `
		SELECT DISTINCT m.name
		FROM models m
		JOIN model_grants mg ON m.id = mg.model_id
		WHERE (mg.team_id = $1 OR (mg.team_id = $1 AND mg.user_id = $2))
		ORDER BY m.name`

	rows, err := r.db.Query(query, teamID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user models: %w", err)
	}
	defer rows.Close()

	var models []string
	for rows.Next() {
		var modelName string
		if err := rows.Scan(&modelName); err != nil {
			return nil, fmt.Errorf("failed to scan model name: %w", err)
		}
		models = append(models, modelName)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating models: %w", err)
	}

	return models, nil
}

// FindUserByEmail finds a user by their email (fallback lookup)
func (r *Repository) FindUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, email, keycloak_user_id, display_name, type, created_at, updated_at
		FROM users 
		WHERE email = $1`

	var user User
	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.KeycloakUserID,
		&user.DisplayName,
		&user.Type,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found with email: %s", email)
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	return &user, nil
}

// GetUserTeamMemberships gets all team memberships for a user
func (r *Repository) GetUserTeamMemberships(ctx context.Context, userID uuid.UUID) ([]TeamMembership, error) {
	query := `
		SELECT team_id, user_id, role, joined_at
		FROM team_memberships 
		WHERE user_id = $1`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user team memberships: %w", err)
	}
	defer rows.Close()

	var memberships []TeamMembership
	for rows.Next() {
		var membership TeamMembership
		err := rows.Scan(
			&membership.TeamID,
			&membership.UserID,
			&membership.Role,
			&membership.JoinedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan team membership: %w", err)
		}
		memberships = append(memberships, membership)
	}

	return memberships, nil
}

// GetTeamByID gets team information by team ID
func (r *Repository) GetTeamByID(ctx context.Context, teamID uuid.UUID) (*Team, error) {
	query := `
		SELECT id, ext_id, name, description, default_policy_id, created_at, updated_at
		FROM teams 
		WHERE id = $1`

	var team Team
	err := r.db.QueryRowContext(ctx, query, teamID).Scan(
		&team.ID,
		&team.ExtID,
		&team.Name,
		&team.Description,
		&team.DefaultPolicyID,
		&team.CreatedAt,
		&team.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("team not found with id: %s", teamID)
		}
		return nil, fmt.Errorf("failed to find team: %w", err)
	}

	return &team, nil
}

// GetUserModelAccess gets all models a user can access (team-wide + user-specific grants)
func (r *Repository) GetUserModelAccess(ctx context.Context, userID uuid.UUID, teamID uuid.UUID) ([]Model, error) {
	query := `
		SELECT DISTINCT m.id, m.name, m.provider, m.route_name, m.status, m.pricing_json, m.created_at, m.updated_at
		FROM models m
		INNER JOIN model_grants mg ON m.id = mg.model_id
		WHERE mg.team_id = $1 AND (mg.user_id IS NULL OR mg.user_id = $2)
		AND m.status = 'published'
		ORDER BY m.name`

	rows, err := r.db.QueryContext(ctx, query, teamID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user model access: %w", err)
	}
	defer rows.Close()

	var models []Model
	for rows.Next() {
		var model Model
		err := rows.Scan(
			&model.ID,
			&model.Name,
			&model.Provider,
			&model.RouteName,
			&model.Status,
			&model.PricingJSON,
			&model.CreatedAt,
			&model.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan model: %w", err)
		}
		models = append(models, model)
	}

	return models, nil
}

// GetPolicyByID gets policy information by policy ID
func (r *Repository) GetPolicyByID(ctx context.Context, policyID uuid.UUID) (*Policy, error) {
	query := `
		SELECT id, name, type, spec, description, created_at, updated_at
		FROM policies 
		WHERE id = $1`

	var policy Policy
	err := r.db.QueryRowContext(ctx, query, policyID).Scan(
		&policy.ID,
		&policy.Name,
		&policy.Type,
		&policy.Spec,
		&policy.Description,
		&policy.CreatedAt,
		&policy.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("policy not found with id: %s", policyID)
		}
		return nil, fmt.Errorf("failed to find policy: %w", err)
	}

	return &policy, nil
}

// CreateModelGrant creates a new model grant for a team
func (r *Repository) CreateModelGrant(ctx context.Context, teamID uuid.UUID, userID *uuid.UUID, modelID, role string) (*ModelGrant, error) {
	grantUUID := uuid.New()
	
	// First, try to find or create the model
	var modelUUID uuid.UUID
	modelQuery := `SELECT id FROM models WHERE name = $1`
	err := r.db.QueryRowContext(ctx, modelQuery, modelID).Scan(&modelUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Model doesn't exist, create it
			modelUUID = uuid.New()
			createModelQuery := `
				INSERT INTO models (id, name, description, provider, model_type, pricing_json, created_at, updated_at)
				VALUES ($1, $2, $3, 'local', 'text', '{}', NOW(), NOW())`
			_, err = r.db.ExecContext(ctx, createModelQuery, modelUUID, modelID, "Auto-created model")
			if err != nil {
				return nil, fmt.Errorf("failed to create model %s: %w", modelID, err)
			}
		} else {
			return nil, fmt.Errorf("failed to lookup model %s: %w", modelID, err)
		}
	}
	
	// Create the model grant
	query := `
		INSERT INTO model_grants (id, team_id, user_id, model_id, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, team_id, user_id, model_id, role`
	
	var grant ModelGrant
	err = r.db.QueryRowContext(ctx, query, grantUUID, teamID, userID, modelUUID, role).Scan(
		&grant.ID, &grant.TeamID, &grant.UserID, &grant.ModelID, &grant.Role)
	
	if err != nil {
		return nil, fmt.Errorf("failed to create model grant: %w", err)
	}
	
	return &grant, nil
}

// UpdateTeam updates team information in the database
func (r *Repository) UpdateTeam(ctx context.Context, teamID string, name, description *string, defaultPolicyID *string) (*Team, error) {
	// Parse team ID
	teamUUID, err := uuid.Parse(teamID)
	if err != nil {
		return nil, fmt.Errorf("invalid team ID format: %w", err)
	}
	
	// Build dynamic query based on provided fields
	var setParts []string
	var args []interface{}
	argIndex := 1
	
	if name != nil {
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, *name)
		argIndex++
	}
	
	if description != nil {
		setParts = append(setParts, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, *description)
		argIndex++
	}
	
	if defaultPolicyID != nil {
		if *defaultPolicyID == "" {
			setParts = append(setParts, fmt.Sprintf("default_policy_id = NULL"))
		} else {
			policyUUID, err := uuid.Parse(*defaultPolicyID)
			if err != nil {
				return nil, fmt.Errorf("invalid policy ID format: %w", err)
			}
			setParts = append(setParts, fmt.Sprintf("default_policy_id = $%d", argIndex))
			args = append(args, policyUUID)
			argIndex++
		}
	}
	
	if len(setParts) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}
	
	// Add updated_at
	setParts = append(setParts, fmt.Sprintf("updated_at = NOW()"))
	
	// Add team ID as last parameter
	args = append(args, teamUUID)
	
	query := fmt.Sprintf(`
		UPDATE teams 
		SET %s
		WHERE id = $%d
		RETURNING id, ext_id, name, description, default_policy_id, created_at, updated_at`,
		strings.Join(setParts, ", "), argIndex)
	
	var team Team
	err = r.db.QueryRowContext(ctx, query, args...).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.DefaultPolicyID, &team.CreatedAt, &team.UpdatedAt)
	
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("team not found with id: %s", teamID)
		}
		return nil, fmt.Errorf("failed to update team: %w", err)
	}
	
	return &team, nil
}

// ListTeamAPIKeys lists all API keys for a team (excludes sensitive salt)
func (r *Repository) ListTeamAPIKeys(ctx context.Context, teamID uuid.UUID) ([]APIKey, error) {
	query := `
		SELECT id, key_prefix, key_hash, team_id, user_id, alias, created_at
		FROM api_keys 
		WHERE team_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to list team API keys: %w", err)
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var key APIKey
		err := rows.Scan(
			&key.ID,
			&key.KeyPrefix,
			&key.KeyHash,
			&key.TeamID,
			&key.UserID,
			&key.Alias,
			&key.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// ListUserAPIKeys lists all API keys for a user across all teams (excludes sensitive salt)
func (r *Repository) ListUserAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKey, error) {
	query := `
		SELECT id, key_prefix, key_hash, team_id, user_id, alias, created_at
		FROM api_keys 
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list user API keys: %w", err)
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var key APIKey
		err := rows.Scan(
			&key.ID,
			&key.KeyPrefix,
			&key.KeyHash,
			&key.TeamID,
			&key.UserID,
			&key.Alias,
			&key.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		keys = append(keys, key)
	}

	return keys, nil
}
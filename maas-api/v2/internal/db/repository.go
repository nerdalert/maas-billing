package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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

func (r *Repository) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	query := `
		SELECT id, email, keycloak_user_id, display_name, type, created_at, updated_at
		FROM users
		WHERE id = $1`

	var user User
	err := r.db.QueryRowContext(ctx, query, userID).Scan(
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
			return nil, fmt.Errorf("user not found with ID: %s", userID)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

// GetAPIKeyByPrefix finds an API key by its prefix (first 8 characters)
func (r *Repository) GetAPIKeyByPrefix(prefix string) (*APIKey, error) {
	log.Printf("DEBUG GetAPIKeyByPrefix: Looking for prefix: %s", prefix)

	// First, let's see what prefixes actually exist in the database
	debugQuery := `SELECT key_prefix FROM api_keys ORDER BY created_at DESC LIMIT 5`
	rows, debugErr := r.db.Query(debugQuery)
	if debugErr == nil {
		defer rows.Close()
		log.Printf("DEBUG GetAPIKeyByPrefix: Recent prefixes in database:")
		for rows.Next() {
			var existingPrefix string
			if err := rows.Scan(&existingPrefix); err == nil {
				log.Printf("DEBUG GetAPIKeyByPrefix: Found prefix in DB: %s", existingPrefix)
			}
		}
	}

	query := `
		SELECT ak.id, ak.team_id, ak.user_id, ak.key_prefix, ak.key_hash, encode(ak.salt, 'hex'), ak.created_at
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
			log.Printf("DEBUG GetAPIKeyByPrefix: No rows found for prefix: %s", prefix)
			return nil, fmt.Errorf("api key not found with prefix: %s", prefix)
		}
		log.Printf("DEBUG GetAPIKeyByPrefix: Query error: %v", err)
		return nil, fmt.Errorf("failed to find api key: %w", err)
	}

	log.Printf("DEBUG GetAPIKeyByPrefix: Found match - keyPrefix: %s, keyHash: %s", apiKey.KeyPrefix, apiKey.KeyHash)
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

// CreateTeam creates a new team in the database with embedded rate limits
func (r *Repository) CreateTeam(ctx context.Context, extID, name, description string, rateLimit int, rateWindow, rateLimitSpec string) (*Team, error) {
	teamUUID := uuid.New()
	query := `
		INSERT INTO teams (id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at`

	var team Team
	err := r.db.QueryRowContext(ctx, query, teamUUID, extID, name, description, rateLimit, rateWindow, rateLimitSpec).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.RateLimit, &team.RateWindow, &team.RateLimitSpec, &team.CreatedAt, &team.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}

	return &team, nil
}

// CreateAPIKey creates a new API key in the database
func (r *Repository) CreateAPIKey(ctx context.Context, keyPrefix, keyHash, salt, teamID, userID, alias string) (*APIKey, error) {
	keyUUID := uuid.New()

	// Look up team by ID or external ID to get internal UUID
	var teamUUID uuid.UUID
	if teamIDParsed, parseErr := uuid.Parse(teamID); parseErr == nil {
		// teamID is already a UUID
		teamUUID = teamIDParsed
	} else {
		// teamID is an external ID, look it up
		team, err := r.GetTeamByExtID(ctx, teamID)
		if err != nil {
			return nil, fmt.Errorf("failed to find team with external ID %s: %w", teamID, err)
		}
		teamUUID = team.ID
	}

	// For now, store plaintext key for direct comparison (TODO: implement Argon2 later)
	// Handle user_id: if provided, try to parse as UUID first, then try keycloak_user_id lookup
	var userUUID *uuid.UUID
	if userID != "" {
		log.Printf("DEBUG CreateAPIKey: Attempting to resolve userID: %s", userID)

		// First try to parse as UUID directly
		if parsedUUID, err := uuid.Parse(userID); err == nil {
			log.Printf("DEBUG CreateAPIKey: Parsed as UUID: %s", parsedUUID)
			// Check if this UUID exists in the users table
			var exists bool
			existsQuery := `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`
			if err := r.db.QueryRowContext(ctx, existsQuery, parsedUUID).Scan(&exists); err == nil && exists {
				log.Printf("DEBUG CreateAPIKey: User UUID found in database: %s", parsedUUID)
				userUUID = &parsedUUID
			} else {
				log.Printf("DEBUG CreateAPIKey: User UUID not found in database or query failed: %v", err)
			}
		} else {
			log.Printf("DEBUG CreateAPIKey: Failed to parse as UUID: %v", err)
		}

		// If UUID parse/lookup failed, try as keycloak_user_id
		if userUUID == nil {
			log.Printf("DEBUG CreateAPIKey: Trying keycloak_user_id lookup for: %s", userID)
			var tempUUID uuid.UUID
			userQuery := `SELECT id FROM users WHERE keycloak_user_id = $1`
			err := r.db.QueryRowContext(ctx, userQuery, userID).Scan(&tempUUID)
			if err == nil {
				log.Printf("DEBUG CreateAPIKey: Found user by keycloak_user_id: %s -> %s", userID, tempUUID)
				userUUID = &tempUUID
			} else {
				log.Printf("DEBUG CreateAPIKey: Keycloak user ID not found: %v", err)
			}
		}

		if userUUID == nil {
			log.Printf("DEBUG CreateAPIKey: User not found by either method, creating team service key")
		} else {
			log.Printf("DEBUG CreateAPIKey: Creating user-specific key for user: %s", *userUUID)
		}
	} else {
		log.Printf("DEBUG CreateAPIKey: No userID provided, creating team service key")
	}

	// Add debug logging for the database insertion
	log.Printf("DEBUG CreateAPIKey: About to insert - keyPrefix: %s, keyHash: %s, salt: %s", keyPrefix, keyHash, salt)
	log.Printf("DEBUG CreateAPIKey: Team UUID: %s, User UUID: %v", teamUUID, userUUID)

	query := `
		INSERT INTO api_keys (id, key_prefix, key_hash, salt, team_id, user_id, alias)
		VALUES ($1, $2, $3, decode($4, 'hex'), $5, $6, $7)
		RETURNING id, key_prefix, key_hash, encode(salt, 'hex'), team_id, user_id, alias, created_at`

	var apiKey APIKey
	err := r.db.QueryRowContext(ctx, query, keyUUID, keyPrefix, keyHash, salt, teamUUID, userUUID, alias).Scan(
		&apiKey.ID, &apiKey.KeyPrefix, &apiKey.KeyHash, &apiKey.Salt, &apiKey.TeamID, &apiKey.UserID, &apiKey.Alias, &apiKey.CreatedAt)

	if err != nil {
		log.Printf("DEBUG CreateAPIKey: Database insertion failed: %v", err)
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}

	// Add debug logging for what was actually stored
	log.Printf("DEBUG CreateAPIKey: Successfully inserted - returned keyPrefix: %s, keyHash: %s", apiKey.KeyPrefix, apiKey.KeyHash)
	log.Printf("DEBUG CreateAPIKey: Returned ID: %s, TeamID: %s", apiKey.ID, apiKey.TeamID)

	return &apiKey, nil
}




// GetTeamByExtID gets team details by external ID
func (r *Repository) GetTeamByExtID(ctx context.Context, extID string) (*Team, error) {
	query := `
		SELECT id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at
		FROM teams
		WHERE ext_id = $1`

	var team Team
	err := r.db.QueryRowContext(ctx, query, extID).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.RateLimit, &team.RateWindow, &team.RateLimitSpec, &team.CreatedAt, &team.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to get team by ext_id: %w", err)
	}

	return &team, nil
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
		SELECT id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at
		FROM teams
		WHERE id = $1`

	var team Team
	err := r.db.QueryRowContext(ctx, query, teamID).Scan(
		&team.ID,
		&team.ExtID,
		&team.Name,
		&team.Description,
		&team.RateLimit,
		&team.RateWindow,
		&team.RateLimitSpec,
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
				INSERT INTO models (id, name, provider, route_name, status, pricing_json, created_at, updated_at)
				VALUES ($1, $2, 'local', $2, 'published', '{}', NOW(), NOW())`
			_, err = r.db.ExecContext(ctx, createModelQuery, modelUUID, modelID)
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
func (r *Repository) UpdateTeam(ctx context.Context, teamID string, name, description *string, rateLimit *int, rateWindow *string) (*Team, error) {
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

	if rateLimit != nil {
		setParts = append(setParts, fmt.Sprintf("rate_limit = $%d", argIndex))
		args = append(args, *rateLimit)
		argIndex++
	}

	if rateWindow != nil {
		setParts = append(setParts, fmt.Sprintf("rate_window = $%d", argIndex))
		args = append(args, *rateWindow)
		argIndex++
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
		RETURNING id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at`,
		strings.Join(setParts, ", "), argIndex)

	var team Team
	err = r.db.QueryRowContext(ctx, query, args...).Scan(
		&team.ID, &team.ExtID, &team.Name, &team.Description, &team.RateLimit, &team.RateWindow, &team.RateLimitSpec, &team.CreatedAt, &team.UpdatedAt)

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

// CreateUser creates a new user in the database
func (r *Repository) CreateUser(ctx context.Context, keycloakUserID, email, displayName string) (*User, error) {
	userUUID := uuid.New()
	query := `
		INSERT INTO users (id, keycloak_user_id, email, display_name, type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'human', NOW(), NOW())
		RETURNING id, email, keycloak_user_id, display_name, type, created_at, updated_at`

	var user User
	err := r.db.QueryRowContext(ctx, query, userUUID, keycloakUserID, email, displayName).Scan(
		&user.ID,
		&user.Email,
		&user.KeycloakUserID,
		&user.DisplayName,
		&user.Type,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return &user, nil
}

// AddUserToTeam adds a user to a team
func (r *Repository) AddUserToTeam(ctx context.Context, userID, teamID uuid.UUID, role string) error {
	query := `
		INSERT INTO team_memberships (team_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, NOW())`

	_, err := r.db.ExecContext(ctx, query, teamID, userID, role)
	if err != nil {
		return fmt.Errorf("failed to add user to team: %w", err)
	}

	return nil
}

// ListTeams lists all teams in the database
func (r *Repository) ListTeams(ctx context.Context) ([]Team, error) {
	query := `
		SELECT id, ext_id, name, description, rate_limit, rate_window, rate_limit_spec, created_at, updated_at
		FROM teams
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var team Team
		err := rows.Scan(
			&team.ID,
			&team.ExtID,
			&team.Name,
			&team.Description,
			&team.RateLimit,
			&team.RateWindow,
			&team.RateLimitSpec,
			&team.CreatedAt,
			&team.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan team: %w", err)
		}
		teams = append(teams, team)
	}

	return teams, nil
}

// DeleteTeamResult contains information about the deleted team and cascaded deletions
type DeleteTeamResult struct {
	TeamID           uuid.UUID `json:"team_id"`
	ExtID            string    `json:"ext_id"`
	Name             string    `json:"name"`
	CascadedKeyCount int       `json:"cascaded_key_count"`
}

// DeleteTeam deletes a team and cascades to all dependent resources
func (r *Repository) DeleteTeam(ctx context.Context, teamID uuid.UUID) (*DeleteTeamResult, error) {
	// Start transaction for atomic operations
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Lock the team row and get team details
	var team Team
	err = tx.QueryRowContext(ctx, `
		SELECT id, ext_id, name, description, created_at, updated_at
		FROM teams
		WHERE id = $1
		FOR UPDATE`,
		teamID).Scan(
		&team.ID,
		&team.ExtID,
		&team.Name,
		&team.Description,
		&team.CreatedAt,
		&team.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("team not found with id: %s", teamID)
		}
		return nil, fmt.Errorf("failed to lock team: %w", err)
	}

	// Count dependent API keys before deletion
	var keyCount int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM api_keys WHERE team_id = $1`,
		teamID).Scan(&keyCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count dependent keys: %w", err)
	}


	// Delete the team (cascades will automatically handle dependent records)
	var deletedExtID, deletedName string
	err = tx.QueryRowContext(ctx, `
		DELETE FROM teams
		WHERE id = $1
		RETURNING ext_id, name`,
		teamID).Scan(&deletedExtID, &deletedName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("team not found with id: %s", teamID)
		}
		return nil, fmt.Errorf("failed to delete team: %w", err)
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	result := &DeleteTeamResult{
		TeamID:           teamID,
		ExtID:            deletedExtID,
		Name:             deletedName,
		CascadedKeyCount: keyCount,
	}

	return result, nil
}

// DeleteAPIKeyResult contains information about the deleted API key
type DeleteAPIKeyResult struct {
	KeyID     string `json:"key_id"`
	KeyPrefix string `json:"key_prefix"`
	Alias     string `json:"alias"`
	TeamID    string `json:"team_id"`
	UserID    string `json:"user_id,omitempty"`
}

// DeleteAPIKeyByPrefix deletes an API key by its prefix
func (r *Repository) DeleteAPIKeyByPrefix(ctx context.Context, keyPrefix string) (*DeleteAPIKeyResult, error) {
	query := `
		DELETE FROM api_keys
		WHERE key_prefix = $1
		RETURNING id, key_prefix, alias, team_id, user_id`

	var result DeleteAPIKeyResult
	var userID *string
	err := r.db.QueryRowContext(ctx, query, keyPrefix).Scan(
		&result.KeyID,
		&result.KeyPrefix,
		&result.Alias,
		&result.TeamID,
		&userID,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("API key not found with prefix: %s", keyPrefix)
		}
		return nil, fmt.Errorf("failed to delete API key: %w", err)
	}

	if userID != nil {
		result.UserID = *userID
	}

	return &result, nil
}

// DeleteAPIKeyByID deletes an API key by its ID
func (r *Repository) DeleteAPIKeyByID(ctx context.Context, keyID uuid.UUID) (*DeleteAPIKeyResult, error) {
	query := `
		DELETE FROM api_keys
		WHERE id = $1
		RETURNING id, key_prefix, alias, team_id, user_id`

	var result DeleteAPIKeyResult
	var userID *string
	err := r.db.QueryRowContext(ctx, query, keyID).Scan(
		&result.KeyID,
		&result.KeyPrefix,
		&result.Alias,
		&result.TeamID,
		&userID,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("API key not found with ID: %s", keyID)
		}
		return nil, fmt.Errorf("failed to delete API key: %w", err)
	}

	if userID != nil {
		result.UserID = *userID
	}

	return &result, nil
}


package db

import (
	"time"

	"github.com/google/uuid"
)

// User represents a user in the system
type User struct {
	ID              uuid.UUID `json:"id"`
	Email           string    `json:"email"`
	KeycloakUserID  string    `json:"keycloak_user_id"`
	DisplayName     string    `json:"display_name"`
	Type            string    `json:"type"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Team represents a team/tenant in the system
type Team struct {
	ID               uuid.UUID  `json:"id"`
	ExtID            string     `json:"ext_id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	DefaultPolicyID  *uuid.UUID `json:"default_policy_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	// DEPRECATED fields for backward compatibility - hidden when nil
	PolicyID         *uuid.UUID  `json:"policy_id,omitempty"`
}

// TeamMembership represents a user's role in a team
type TeamMembership struct {
	TeamID   uuid.UUID `json:"team_id"`
	UserID   uuid.UUID `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// Model represents an AI model in the catalog
type Model struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Provider    string    `json:"provider"`
	RouteName   string    `json:"route_name"`
	Status      string    `json:"status"`
	PricingJSON string    `json:"pricing_json"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ModelGrant represents model access permissions
type ModelGrant struct {
	ID      uuid.UUID  `json:"id"`
	TeamID  uuid.UUID  `json:"team_id"`
	UserID  *uuid.UUID `json:"user_id,omitempty"` // NULL for team-wide grants
	ModelID uuid.UUID  `json:"model_id"`
	Role    string     `json:"role"`
}

// Policy represents a rate limiting policy
type Policy struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Version     string    `json:"version"`
	SpecJSON    string    `json:"spec_json"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// DEPRECATED fields for backward compatibility
	Type        string    `json:"type,omitempty"`
	Spec        string    `json:"spec,omitempty"`
	Description string    `json:"description,omitempty"`
}

// APIKey represents an API key for authentication
type APIKey struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"team_id"`
	UserID    *string   `json:"user_id,omitempty"` // NULL for team service keys
	KeyPrefix string    `json:"key_prefix"`
	KeyHash   string    `json:"key_hash"`
	Salt      string    `json:"salt"`
	Alias     string    `json:"alias"`
	CreatedAt time.Time `json:"created_at"`
}

// DEPRECATED: Legacy types for backward compatibility - will be removed
type IdentityLookupRequest struct {
	Sub   string `json:"sub" binding:"required"`   // JWT subject (Keycloak user ID)
	Email string `json:"email" binding:"required"` // JWT email claim
}

type IdentityLookupResponse struct {
	UserID        uuid.UUID `json:"user_id"`
	TeamID        uuid.UUID `json:"team_id"`
	Plan          string    `json:"plan"`
	Groups        []string  `json:"groups"`
	ModelsAllowed []string  `json:"models_allowed"`
	APIKeyID      *uuid.UUID `json:"api_key_id,omitempty"`
}


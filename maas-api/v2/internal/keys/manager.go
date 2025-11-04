package keys

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/db"
)

// Manager handles API key operations (database-driven only)
type Manager struct {
	repo *db.Repository
}

// NewManager creates a new key manager
func NewManager(repo *db.Repository) *Manager {
	return &Manager{
		repo: repo,
	}
}

// CreateTeamKey creates a new API key for a team member (database-driven)
func (m *Manager) CreateTeamKey(teamID string, req *CreateTeamKeyRequest) (*CreateTeamKeyResponse, error) {
	ctx := context.Background()

	// Parse team ID
	teamUUID, err := uuid.Parse(teamID)
	if err != nil {
		return nil, fmt.Errorf("invalid team ID: %w", err)
	}

	// Parse user ID
	userUUID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}

	// Validate team exists
	_, err = m.repo.GetTeamByID(ctx, teamUUID)
	if err != nil {
		return nil, fmt.Errorf("team not found: %w", err)
	}

	// Generate API key
	apiKey, keyHash, salt, keyPrefix, err := m.generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	log.Printf("DEBUG: Generated API key: %s", apiKey)
	log.Printf("DEBUG: Generated prefix: %s", keyPrefix)
	log.Printf("DEBUG: Generated hash: %s", keyHash)
	log.Printf("DEBUG: Generated salt: %s", salt)

	// Store in database
	var userIDStr string
	if userUUID != (uuid.UUID{}) {
		userIDStr = userUUID.String()
	}
	dbKey, err := m.repo.CreateAPIKey(ctx, keyPrefix, keyHash, salt, teamUUID.String(), userIDStr, req.Alias)
	if err != nil {
		return nil, fmt.Errorf("failed to store API key: %w", err)
	}

	return &CreateTeamKeyResponse{
		ID:      dbKey.ID,
		APIKey:  apiKey,
		KeyHash: keyHash,
		TeamID:  teamID,
		UserID:  req.UserID,
		Alias:   req.Alias,
	}, nil
}

// generateAPIKey generates a new API key with hash and salt
func (m *Manager) generateAPIKey() (apiKey, keyHash, salt, keyPrefix string, err error) {
	// Generate 32 bytes (256 bits) of random data
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", "", "", "", fmt.Errorf("failed to generate random key: %w", err)
	}

	// Convert to base64 URL-safe string (matches old working key format)
	apiKey = base64.RawURLEncoding.EncodeToString(keyBytes)

	// Extract prefix (first 8 characters)
	keyPrefix = apiKey[:8]

	// Generate salt
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", "", "", "", fmt.Errorf("failed to generate salt: %w", err)
	}
	salt = hex.EncodeToString(saltBytes)

	// Store the actual key for now (like the old system)
	// TODO: implement proper Argon2 hashing later
	keyHash = apiKey

	return apiKey, keyHash, salt, keyPrefix, nil
}

package handlers

import (
	"context"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/db"
	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/keys"
)

// KeysHandler handles key-related endpoints
type KeysHandler struct {
	keyMgr *keys.Manager
	repo   *db.Repository
}

type userKeyResponse struct {
	ID        string `json:"id"`
	Alias     string `json:"alias"`
	CreatedAt string `json:"created_at"`
	KeyPrefix string `json:"key_prefix"`
	Key       string `json:"key"`
	TeamID    string `json:"team_id"`
	TeamExtID string `json:"team_ext_id,omitempty"`
	TeamName  string `json:"team_name,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
}

// NewKeysHandler creates a new keys handler
func NewKeysHandler(keyMgr *keys.Manager, repo *db.Repository) *KeysHandler {
	return &KeysHandler{
		keyMgr: keyMgr,
		repo:   repo,
	}
}

// resolveTeamRef resolves a team reference (UUID or external ID) to team info
func (h *KeysHandler) resolveTeamRef(teamRef string) (*db.Team, error) {
	// Check if it's a UUID format
	if _, err := uuid.Parse(teamRef); err == nil {
		// It's a UUID, look up by ID
		return h.repo.GetTeamByID(context.Background(), uuid.MustParse(teamRef))
	} else {
		// It's an external ID, look up by ext_id
		return h.repo.GetTeamByExtID(context.Background(), teamRef)
	}
}

// CreateTeamKey handles POST /teams/:team_id/keys
func (h *KeysHandler) CreateTeamKey(c *gin.Context) {
	teamRef := c.Param("team_id") // Can be UUID or external ID

	// Extract JWT user context from headers set by Authorino
	adminUserID, _ := c.Get("user_id")
	adminEmail, _ := c.Get("user_email")
	adminRoles, _ := c.Get("user_roles")

	log.Printf("🎯 CreateTeamKey: Processing request for team %s from admin %v (email: %v, roles: %v)",
		teamRef, adminUserID, adminEmail, adminRoles)

	var req keys.CreateTeamKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("❌ CreateTeamKey: Invalid JSON request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("CreateTeamKey: Request data - UserID: %s, Alias: %s",
		req.UserID, req.Alias)

	// Resolve team reference to get team info
	log.Printf("🔍 CreateTeamKey: Resolving team reference %s...", teamRef)
	team, err := h.resolveTeamRef(teamRef)
	if err != nil {
		log.Printf("❌ CreateTeamKey: Team %s not found: %v", teamRef, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}
	log.Printf("✅ CreateTeamKey: Team resolved - ID: %s, ExtID: %s, Name: %s", team.ID, team.ExtID, team.Name)

	// Use the team's internal UUID for key creation (database-first approach)
	log.Printf("🔄 CreateTeamKey: Creating API key in database for team UUID %s...", team.ID)
	response, err := h.keyMgr.CreateTeamKey(team.ID.String(), &req)
	if err != nil {
		log.Printf("❌ CreateTeamKey: Failed to create team key: %v", err)
		if strings.Contains(err.Error(), "already has an active API key") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create API key"})
		}
		return
	}

	log.Printf("✅ CreateTeamKey: Team API key created successfully for user %s in team %s by admin %v",
		req.UserID, team.ExtID, adminUserID)
	c.JSON(http.StatusOK, response)
}

// ListTeamKeys handles GET /teams/:team_id/keys
func (h *KeysHandler) ListTeamKeys(c *gin.Context) {
	teamRef := c.Param("team_id")

	// Extract JWT user context
	userID, _ := c.Get("user_id")

	log.Printf("🎯 ListTeamKeys: Processing request for team %s from user %v", teamRef, userID)

	// Resolve team reference (UUID or external ID)
	team, err := h.resolveTeamRef(teamRef)
	if err != nil {
		log.Printf("❌ ListTeamKeys: Team %s not found: %v", teamRef, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	// Get team API keys from database
	keys, err := h.repo.ListTeamAPIKeys(context.Background(), team.ID)
	if err != nil {
		log.Printf("❌ ListTeamKeys: Failed to get team keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get team keys"})
		return
	}

	log.Printf("✅ ListTeamKeys: Found %d keys for team %s", len(keys), team.ExtID)
	c.JSON(http.StatusOK, gin.H{
		"team_id":     team.ID,
		"team_ext_id": team.ExtID,
		"team_name":   team.Name,
		"keys":        keys,
		"total_keys":  len(keys),
	})
}

// DeleteAPIKey handles DELETE /keys/:key_prefix
func (h *KeysHandler) DeleteAPIKey(c *gin.Context) {
	keyPrefix := c.Param("key_name") // Using key_name for backward compatibility

	// Extract JWT user context
	userID, _ := c.Get("user_id")
	userEmail, _ := c.Get("user_email")

	log.Printf("DeleteAPIKey: Processing request for key %s from user %v (email: %v)", keyPrefix, userID, userEmail)

	ctx := context.Background()

	// Delete API key using database-first approach
	result, err := h.repo.DeleteAPIKeyByPrefix(ctx, keyPrefix)
	if err != nil {
		if strings.Contains(err.Error(), "API key not found") {
			log.Printf("DeleteAPIKey: Key %s not found: %v", keyPrefix, err)
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
			return
		}
		log.Printf("DeleteAPIKey: Failed to delete key %s: %v", keyPrefix, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete API key"})
		return
	}

	log.Printf("DeleteAPIKey: Key %s (alias: %s) deleted successfully from team %s by user %v",
		result.KeyPrefix, result.Alias, result.TeamID, userID)

	c.JSON(http.StatusOK, gin.H{
		"message":    "API key deleted successfully",
		"key_id":     result.KeyID,
		"key_prefix": result.KeyPrefix,
		"alias":      result.Alias,
		"team_id":    result.TeamID,
		"deleted_by": userID,
	})
}

// ListUserKeys handles GET /users/:user_id/keys
func (h *KeysHandler) ListUserKeys(c *gin.Context) {
	userRef := c.Param("user_id")

	// Extract requester from JWT context (Keycloak user ID)
	reqUserIDIface, _ := c.Get("user_id")
	requesterKeycloakUserID, _ := reqUserIDIface.(string)

	// Resolve requester internal UUID (for "me" support)
	requester, err := h.repo.FindUserByKeycloakID(context.Background(), requesterKeycloakUserID)
	if err != nil {
		log.Printf("❌ ListUserKeys: could not identify requester: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not identify requester"})
		return
	}

	// Determine target user UUID
	var userUUID uuid.UUID
	if userRef == "me" {
		userUUID = requester.ID
	} else {
		parsed, parseErr := uuid.Parse(userRef)
		if parseErr != nil {
			log.Printf("❌ ListUserKeys: Invalid user ID format: %v", parseErr)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
			return
		}
		userUUID = parsed
	}

	// Get user info for ownership display
	targetUser, err := h.repo.GetUserByID(context.Background(), userUUID)
	if err != nil {
		log.Printf("❌ ListUserKeys: Failed to get target user info: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user info"})
		return
	}

	// Get user API keys from database
	keys, err := h.repo.ListUserAPIKeys(context.Background(), userUUID)
	if err != nil {
		log.Printf("❌ ListUserKeys: Failed to get user keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user keys"})
		return
	}

	teamCache := make(map[string]*db.Team)
	responseKeys := make([]userKeyResponse, 0, len(keys))

	for _, key := range keys {
		var (
			teamExtID string
			teamName  string
		)

		if key.TeamID != "" {
			if cached, ok := teamCache[key.TeamID]; ok {
				teamExtID = cached.ExtID
				teamName = cached.Name
			} else {
				parsedID, parseErr := uuid.Parse(key.TeamID)
				if parseErr != nil {
					log.Printf("⚠️ ListUserKeys: Invalid team UUID %s: %v", key.TeamID, parseErr)
				} else {
					team, teamErr := h.repo.GetTeamByID(context.Background(), parsedID)
					if teamErr != nil {
						log.Printf("⚠️ ListUserKeys: Failed to load team %s: %v", key.TeamID, teamErr)
					} else {
						teamCache[key.TeamID] = team
						teamExtID = team.ExtID
						teamName = team.Name
					}
				}
			}
		}

		responseKeys = append(responseKeys, userKeyResponse{
			ID:        key.ID,
			Alias:     key.Alias,
			CreatedAt: key.CreatedAt.UTC().Format(time.RFC3339),
			KeyPrefix: key.KeyPrefix,
			Key:       decodeStoredKey(key.KeyHash),
			TeamID:    key.TeamID,
			TeamExtID: teamExtID,
			TeamName:  teamName,
			UserID:    targetUser.ID.String(),
			UserEmail: targetUser.Email,
		})
	}

	log.Printf("✅ ListUserKeys: Found %d keys for user %s", len(keys), userUUID.String())
	c.JSON(http.StatusOK, gin.H{
		"user_id":    userUUID.String(),
		"keys":       responseKeys,
		"total_keys": len(responseKeys),
	})
}

func decodeStoredKey(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "\\x") {
		hexStr := raw[2:]
		decoded, err := hex.DecodeString(hexStr)
		if err != nil {
			log.Printf("⚠️ decodeStoredKey: failed to decode key: %v", err)
			return raw
		}
		return strings.TrimRight(string(decoded), "\x00")
	}
	return raw
}

// CreateUserKeyRequest defines the request body for creating a user-specific API key
type CreateUserKeyRequest struct {
	Alias  string `json:"alias" binding:"required"`
	TeamID string `json:"team_id"` // Optional: if not provided, uses user's default team
}

// CreateUserKey handles POST /users/:user_id/keys
func (h *KeysHandler) CreateUserKey(c *gin.Context) {
	userRef := c.Param("user_id")

	// Extract JWT user context
	reqUserIDIface, _ := c.Get("user_id")
	requesterKeycloakUserID, _ := reqUserIDIface.(string)
	rolesIface, _ := c.Get("user_roles")
	requesterRoles, _ := rolesIface.([]string)

	// Find the requester's internal user ID from their keycloak ID
	requester, err := h.repo.FindUserByKeycloakID(context.Background(), requesterKeycloakUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not identify requester"})
		return
	}

	// Determine target user UUID (support "me")
	var targetUserUUID uuid.UUID
	if userRef == "me" {
		targetUserUUID = requester.ID
	} else {
		parsed, parseErr := uuid.Parse(userRef)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
			return
		}
		targetUserUUID = parsed
	}

	// Authorize: user can only create keys for themselves, unless they are an admin
	isAdmin := false
	for _, role := range requesterRoles {
		if role == "maas-admin" {
			isAdmin = true
			break
		}
	}

	if !isAdmin && requester.ID != targetUserUUID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only create API keys for yourself"})
		return
	}

	var req CreateUserKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var teamID uuid.UUID
	if req.TeamID != "" {
		// If team_id is provided, resolve it (could be UUID or external ID)
		team, err := h.resolveTeamRef(req.TeamID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Team not found"})
			return
		}
		teamID = team.ID
	} else {
		// If team_id is not provided, find the user's default team
		memberships, err := h.repo.GetUserTeamMemberships(context.Background(), targetUserUUID)
		if err != nil || len(memberships) == 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to find user's default team"})
			return
		}
		// For now, just use the first team as the default
		teamID = memberships[0].TeamID
	}

	// Create the key
	createReq := &keys.CreateTeamKeyRequest{
		UserID: targetUserUUID.String(),
		Alias:  req.Alias,
	}
	response, err := h.keyMgr.CreateTeamKey(teamID.String(), createReq)
	if err != nil {
		log.Printf("Failed to create user key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create API key"})
		return
	}

	c.JSON(http.StatusOK, response)
}

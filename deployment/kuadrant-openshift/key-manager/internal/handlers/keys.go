package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/db"
	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/keys"
	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/teams"
)

// KeysHandler handles key-related endpoints
type KeysHandler struct {
	keyMgr  *keys.Manager
	teamMgr *teams.Manager
	repo    *db.Repository
}

// NewKeysHandler creates a new keys handler
func NewKeysHandler(keyMgr *keys.Manager, teamMgr *teams.Manager, repo *db.Repository) *KeysHandler {
	return &KeysHandler{
		keyMgr:  keyMgr,
		teamMgr: teamMgr,
		repo:    repo,
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

// DeleteTeamKey handles DELETE /keys/:key_name
func (h *KeysHandler) DeleteTeamKey(c *gin.Context) {
	keyName := c.Param("key_name")

	keyName, teamID, err := h.keyMgr.DeleteTeamKey(keyName)
	if err != nil {
		log.Printf("Failed to delete team key: %v", err)
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		} else if strings.Contains(err.Error(), "not associated with a team") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "API key is not associated with a team"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete API key"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "API key deleted successfully",
		"key_name": keyName,
		"team_id":  teamID,
	})
}

// ListUserKeys handles GET /users/:user_id/keys
func (h *KeysHandler) ListUserKeys(c *gin.Context) {
	userRef := c.Param("user_id")
	
	// Extract JWT user context
	requesterUserID, _ := c.Get("user_id")
	
	log.Printf("🎯 ListUserKeys: Processing request for user %s from requester %v", userRef, requesterUserID)

	// Parse user ID as UUID
	userUUID, err := uuid.Parse(userRef)
	if err != nil {
		log.Printf("❌ ListUserKeys: Invalid user ID format: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}

	// Get user API keys from database
	keys, err := h.repo.ListUserAPIKeys(context.Background(), userUUID)
	if err != nil {
		log.Printf("❌ ListUserKeys: Failed to get user keys: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user keys"})
		return
	}

	log.Printf("✅ ListUserKeys: Found %d keys for user %s", len(keys), userRef)
	c.JSON(http.StatusOK, gin.H{
		"user_id":    userRef,
		"keys":       keys,
		"total_keys": len(keys),
	})
}

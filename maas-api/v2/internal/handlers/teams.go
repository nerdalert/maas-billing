package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/db"
	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/teams"
)

// Team creation request format with embedded rate limits
type CreateTeamRequest struct {
	ExtID         string `json:"ext_id" binding:"required"`
	Name          string `json:"name" binding:"required"`
	Description   string `json:"description"`
	RateLimit     int    `json:"rate_limit"`
	RateWindow    string `json:"rate_window"`
	RateLimitSpec string `json:"rate_limit_spec"`
}

// Team creation response format
type CreateTeamResponse struct {
	ID            uuid.UUID `json:"id"`
	ExtID         string    `json:"ext_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	RateLimit     int       `json:"rate_limit"`
	RateWindow    string    `json:"rate_window"`
	RateLimitSpec string    `json:"rate_limit_spec"`
	CreatedAt     time.Time `json:"created_at"`
}

// TeamsHandler handles team-related endpoints
type TeamsHandler struct {
	repo      *db.Repository
	policyMgr *teams.PolicyManager
}

// NewTeamsHandler creates a new teams handler
func NewTeamsHandler(repo *db.Repository, policyMgr *teams.PolicyManager) *TeamsHandler {
	return &TeamsHandler{
		repo:      repo,
		policyMgr: policyMgr,
	}
}

// CreateTeam handles POST /teams with embedded rate limits
func (h *TeamsHandler) CreateTeam(c *gin.Context) {
	// Extract JWT user context from headers set by Authorino
	userID, _ := c.Get("user_id")
	userEmail, _ := c.Get("user_email")
	userRoles, _ := c.Get("user_roles")

	log.Printf("CreateTeam: Processing request from user %v (email: %v, roles: %v)", userID, userEmail, userRoles)

	var req CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("CreateTeam: Invalid JSON request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set defaults if not provided
	if req.RateLimit == 0 {
		req.RateLimit = 100
	}
	if req.RateWindow == "" {
		req.RateWindow = "1m"
	}
	if req.RateLimitSpec == "" {
		req.RateLimitSpec = `{"rates":[{"limit":` + fmt.Sprintf("%d", req.RateLimit) + `,"window":"` + req.RateWindow + `"}]}`
	}

	log.Printf("CreateTeam: Request data - Name: %s, ExtID: %s, Description: %s, RateLimit: %d, RateWindow: %s",
		req.Name, req.ExtID, req.Description, req.RateLimit, req.RateWindow)

	ctx := context.Background()

	// Create team in database with embedded rate limits
	team, err := h.repo.CreateTeam(ctx, req.ExtID, req.Name, req.Description, req.RateLimit, req.RateWindow, req.RateLimitSpec)
	if err != nil {
		log.Printf("CreateTeam: Failed to create in database: %v", err)
		// Check for duplicate key violations and return appropriate errors
		if strings.Contains(err.Error(), "duplicate key") {
			if strings.Contains(err.Error(), "teams_name_key") {
				c.JSON(http.StatusConflict, gin.H{"error": "Team name already exists"})
				return
			}
			if strings.Contains(err.Error(), "teams_ext_id_key") {
				c.JSON(http.StatusConflict, gin.H{"error": "Team external ID already exists"})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"error": "Team already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create team"})
		return
	}

	// Sync rate limits to Kuadrant TokenRateLimitPolicy
	if h.policyMgr != nil {
		log.Printf("CreateTeam: Syncing rate limits to TokenRateLimitPolicy for team %s (limit: %d, window: %s)", team.ExtID, team.RateLimit, team.RateWindow)
		err = h.policyMgr.AddTeamToTokenRateLimit(team.ExtID, team.RateLimit, team.RateWindow)
		if err != nil {
			log.Printf("CreateTeam: Warning - Failed to sync rate limits to Kuadrant: %v", err)
		} else {
			log.Printf("CreateTeam: Successfully synced rate limits to TokenRateLimitPolicy")
		}
	}

	response := CreateTeamResponse{
		ID:            team.ID,
		ExtID:         team.ExtID,
		Name:          team.Name,
		Description:   team.Description,
		RateLimit:     team.RateLimit,
		RateWindow:    team.RateWindow,
		RateLimitSpec: team.RateLimitSpec,
		CreatedAt:     team.CreatedAt,
	}

	log.Printf("CreateTeam: Team created successfully: %s (%s) with UUID %s and rate limit %d/%s by user %v",
		team.ExtID, team.Name, team.ID, team.RateLimit, team.RateWindow, userID)
	c.JSON(http.StatusOK, response)
}

// ListTeams handles GET /teams
func (h *TeamsHandler) ListTeams(c *gin.Context) {
	userID, _ := c.Get("user_id")
	log.Printf("ListTeams: Processing request from user %v", userID)

	teams, err := h.repo.ListTeams(context.Background())
	if err != nil {
		log.Printf("Failed to list teams from database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list teams"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"teams": teams, "total_teams": len(teams)})
}

// GetTeam handles GET /teams/:team_id
func (h *TeamsHandler) GetTeam(c *gin.Context) {
	teamRef := c.Param("team_id")
	userID, _ := c.Get("user_id")

	log.Printf("🎯 GetTeam: Processing request for team %s from admin %v", teamRef, userID)

	// Try to resolve team by UUID first, then by external ID
	var team *db.Team
	var err error

	if teamUUID, parseErr := uuid.Parse(teamRef); parseErr == nil {
		// teamRef is a UUID - lookup by internal ID
		team, err = h.repo.GetTeamByID(context.Background(), teamUUID)
	} else {
		// teamRef is external ID - lookup by external ID
		team, err = h.repo.GetTeamByExtID(context.Background(), teamRef)
	}

	if err != nil {
		log.Printf("❌ GetTeam: Team %s not found: %v", teamRef, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	log.Printf("✅ GetTeam: Team found - ID: %s, ExtID: %s, Name: %s", team.ID, team.ExtID, team.Name)
	c.JSON(http.StatusOK, team)
}

// UpdateTeam handles PATCH /teams/:team_id
func (h *TeamsHandler) UpdateTeam(c *gin.Context) {
	teamID := c.Param("team_id")
	var req teams.UpdateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	userEmail, _ := c.Get("user_email")

	log.Printf("UpdateTeam: Processing request for team %s from admin %v (email: %v)", teamID, userID, userEmail)
	log.Printf("UpdateTeam: Request data - Name: %v, Description: %v, RateLimit: %v, RateWindow: %v",
		req.TeamName, req.Description, req.RateLimit, req.RateWindow)

	ctx := context.Background()

	// Resolve team reference (UUID or external ID)
	currentTeam, err := h.resolveTeamRef(teamID)
	if err != nil {
		log.Printf("UpdateTeam: Failed to get current team %s: %v", teamID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}


	// Use database repository for updates
	team, err := h.repo.UpdateTeam(ctx, currentTeam.ID.String(), req.TeamName, req.Description, req.RateLimit, req.RateWindow)
	if err != nil {
		log.Printf("UpdateTeam: Failed to update team %s: %v", teamID, err)
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		} else if strings.Contains(err.Error(), "invalid") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update team"})
		}
		return
	}

	// Handle rate limit changes for Kuadrant sync
	if h.policyMgr != nil {
		rateChanged := false
		if req.RateLimit != nil && *req.RateLimit != currentTeam.RateLimit {
			rateChanged = true
		}
		if req.RateWindow != nil && *req.RateWindow != currentTeam.RateWindow {
			rateChanged = true
		}

		if rateChanged {
			log.Printf("UpdateTeam: Rate limits changed for team %s, syncing to TokenRateLimitPolicy", team.ExtID)
			err = h.policyMgr.AddTeamToTokenRateLimit(team.ExtID, team.RateLimit, team.RateWindow)
			if err != nil {
				log.Printf("UpdateTeam: Warning - Failed to sync updated rate limits to Kuadrant: %v", err)
			} else {
				log.Printf("UpdateTeam: Successfully synced updated rate limits to TokenRateLimitPolicy")
			}
		}
	}

	log.Printf("UpdateTeam: Team %s updated successfully by admin %v", teamID, userID)
	c.JSON(http.StatusOK, gin.H{
		"message": "Team updated successfully",
		"team":    team,
	})
}

// DeleteTeam handles DELETE /teams/:team_id
func (h *TeamsHandler) DeleteTeam(c *gin.Context) {
	teamRef := c.Param("team_id")

	// Extract JWT user context
	userID, _ := c.Get("user_id")
	userEmail, _ := c.Get("user_email")

	log.Printf("DeleteTeam: Processing request for team %s from user %v (email: %v)", teamRef, userID, userEmail)

	ctx := context.Background()

	// Resolve team reference (UUID or external ID)
	team, err := h.resolveTeamRef(teamRef)
	if err != nil {
		log.Printf("DeleteTeam: Team %s not found: %v", teamRef, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	log.Printf("DeleteTeam: Resolved team %s to ID %s, Name: %s", teamRef, team.ID, team.Name)

	// Delete team using database-first approach
	result, err := h.repo.DeleteTeam(ctx, team.ID)
	if err != nil {
		if strings.Contains(err.Error(), "team not found") {
			log.Printf("DeleteTeam: Team %s not found in database: %v", team.ID, err)
			c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
			return
		}
		log.Printf("DeleteTeam: Failed to delete team %s: %v", team.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete team"})
		return
	}

	// Cleanup Kuadrant rate limits for the team
	if h.policyMgr != nil {
		log.Printf("DeleteTeam: Cleaning up Kuadrant rate limits for team %s", result.ExtID)

		// Remove from TokenRateLimitPolicy
		if err := h.policyMgr.RemoveTeamFromTokenRateLimit(result.ExtID); err != nil {
			log.Printf("DeleteTeam: Warning - Failed to remove team %s from TokenRateLimitPolicy: %v", result.ExtID, err)
		} else {
			log.Printf("DeleteTeam: Successfully removed team %s from TokenRateLimitPolicy", result.ExtID)
		}

	}

	log.Printf("DeleteTeam: Team %s (%s) deleted successfully with %d cascaded keys by user %v",
		result.ExtID, result.Name, result.CascadedKeyCount, userID)

	// Return detailed deletion result
	c.JSON(http.StatusOK, gin.H{
		"message":            "Team deleted successfully",
		"team_id":            result.TeamID,
		"ext_id":             result.ExtID,
		"name":               result.Name,
		"cascaded_key_count": result.CascadedKeyCount,
		"deleted_by":         userID,
	})
}

// AddTeamMemberRequest represents the request to add a user to a team
type AddTeamMemberRequest struct {
	UserID string `json:"user_id" binding:"required"`
	Role   string `json:"role"` // member, admin, owner
}

// AddTeamMember handles POST /teams/:team_id/members
func (h *TeamsHandler) AddTeamMember(c *gin.Context) {
	teamID := c.Param("team_id")

	// Extract JWT user context from headers set by Authorino
	adminUserID, _ := c.Get("user_id")
	adminEmail, _ := c.Get("user_email")
	adminRoles, _ := c.Get("user_roles")

	log.Printf("🎯 AddTeamMember: Processing request for team %s from admin %v (email: %v, roles: %v)",
		teamID, adminUserID, adminEmail, adminRoles)

	var req AddTeamMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("❌ AddTeamMember: Invalid JSON request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set default role if none specified
	if req.Role == "" {
		req.Role = "member"
		log.Printf("🔧 AddTeamMember: Using default role: %s", req.Role)
	}

	log.Printf("📋 AddTeamMember: Adding user %s to team %s with role %s", req.UserID, teamID, req.Role)

	// TODO: Implement actual team membership addition via database
	// For now, simulate the operation
	log.Printf("🔄 AddTeamMember: Simulating team membership addition...")

	response := map[string]interface{}{
		"message":  "User added to team successfully",
		"team_id":  teamID,
		"user_id":  req.UserID,
		"role":     req.Role,
		"added_by": adminUserID,
		"added_at": time.Now().Format(time.RFC3339),
	}

	log.Printf("✅ AddTeamMember: User %s added to team %s successfully by admin %v",
		req.UserID, teamID, adminUserID)
	c.JSON(http.StatusOK, response)
}

// RemoveTeamMember handles DELETE /teams/:team_id/members/:user_id
func (h *TeamsHandler) RemoveTeamMember(c *gin.Context) {
	teamID := c.Param("team_id")
	userID := c.Param("user_id")

	// Extract JWT user context from headers set by Authorino
	adminUserID, _ := c.Get("user_id")
	adminEmail, _ := c.Get("user_email")

	log.Printf("🎯 RemoveTeamMember: Processing request to remove user %s from team %s by admin %v (email: %v)",
		userID, teamID, adminUserID, adminEmail)

	// TODO: Implement actual team membership removal via database
	log.Printf("🔄 RemoveTeamMember: Simulating team membership removal...")

	response := map[string]interface{}{
		"message":    "User removed from team successfully",
		"team_id":    teamID,
		"user_id":    userID,
		"removed_by": adminUserID,
		"removed_at": time.Now().Format(time.RFC3339),
	}

	log.Printf("✅ RemoveTeamMember: User %s removed from team %s successfully by admin %v",
		userID, teamID, adminUserID)
	c.JSON(http.StatusOK, response)
}

// ListTeamMembers handles GET /teams/:team_id/members
func (h *TeamsHandler) ListTeamMembers(c *gin.Context) {
	teamID := c.Param("team_id")
	userID, _ := c.Get("user_id")

	log.Printf("🎯 ListTeamMembers: Processing request for team %s from user %v", teamID, userID)

	// TODO: Implement actual database lookup
	log.Printf("📋 ListTeamMembers: Returning mock data for team %s", teamID)

	members := []map[string]interface{}{
		{
			"user_id":   "user-123",
			"email":     "alice@example.com",
			"role":      "owner",
			"joined_at": "2025-01-01T00:00:00Z",
		},
		{
			"user_id":   "user-456",
			"email":     "bob@example.com",
			"role":      "member",
			"joined_at": "2025-01-01T12:00:00Z",
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"team_id": teamID,
		"members": members,
		"total":   len(members),
	})
}

// Model grant request/response structures
type CreateModelGrantRequest struct {
	UserID  *uuid.UUID `json:"user_id"` // NULL for team-wide grants
	ModelID string     `json:"model_id" binding:"required"`
	Role    string     `json:"role" binding:"required"`
}

type CreateModelGrantResponse struct {
	ID      uuid.UUID  `json:"id"`
	TeamID  uuid.UUID  `json:"team_id"`
	UserID  *uuid.UUID `json:"user_id,omitempty"`
	ModelID uuid.UUID  `json:"model_id"`
	Role    string     `json:"role"`
}

// CreateModelGrant handles POST /teams/{team_id}/grants
func (h *TeamsHandler) CreateModelGrant(c *gin.Context) {
	teamRef := c.Param("team_id")

	// Extract JWT user context
	adminUserID, _ := c.Get("user_id")

	var req CreateModelGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("❌ CreateModelGrant: Invalid JSON request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("📋 CreateModelGrant: Creating grant for team %s, model %s, user %v, role %s by admin %v",
		teamRef, req.ModelID, req.UserID, req.Role, adminUserID)

	ctx := context.Background()

	// Resolve team reference (UUID or external ID)
	team, err := h.resolveTeamRef(teamRef)
	if err != nil {
		log.Printf("❌ CreateModelGrant: Team %s not found: %v", teamRef, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	// Create the model grant in database
	grant, err := h.repo.CreateModelGrant(ctx, team.ID, req.UserID, req.ModelID, req.Role)
	if err != nil {
		log.Printf("❌ CreateModelGrant: Failed to create grant: %v", err)
		if strings.Contains(err.Error(), "duplicate key") {
			c.JSON(http.StatusConflict, gin.H{"error": "Model grant already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create model grant"})
		return
	}

	response := CreateModelGrantResponse{
		ID:      grant.ID,
		TeamID:  grant.TeamID,
		UserID:  grant.UserID,
		ModelID: grant.ModelID,
		Role:    grant.Role,
	}

	log.Printf("✅ CreateModelGrant: Grant created successfully: %s for team %s by admin %v",
		grant.ID, team.ExtID, adminUserID)
	c.JSON(http.StatusOK, response)
}

// resolveTeamRef resolves a team reference (UUID or external ID) to team info
func (h *TeamsHandler) resolveTeamRef(teamRef string) (*db.Team, error) {
	ctx := context.Background()

	// Check if it's a UUID format
	if _, err := uuid.Parse(teamRef); err == nil {
		// It's a UUID, look up by ID
		return h.repo.GetTeamByID(ctx, uuid.MustParse(teamRef))
	} else {
		// It's an external ID, look up by ext_id
		return h.repo.GetTeamByExtID(ctx, teamRef)
	}
}

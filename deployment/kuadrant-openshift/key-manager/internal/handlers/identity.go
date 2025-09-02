package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/db"
)

// IdentityHandler handles identity lookup for Authorino
type IdentityHandler struct {
	repo *db.Repository
}

// NewIdentityHandler creates a new identity handler
func NewIdentityHandler(repo *db.Repository) *IdentityHandler {
	return &IdentityHandler{
		repo: repo,
	}
}

// IdentityLookup handles POST /identity/lookup for Authorino metadata enrichment
func (h *IdentityHandler) IdentityLookup(c *gin.Context) {
	var req db.IdentityLookupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Invalid identity lookup request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	ctx := context.Background()

	// Step 1: Find user by Keycloak user ID (JWT sub), fallback to email
	user, err := h.repo.FindUserByKeycloakID(ctx, req.Sub)
	if err != nil {
		// Fallback to email lookup
		if req.Email != "" {
			user, err = h.repo.FindUserByEmail(ctx, req.Email)
		}
		if err != nil {
			log.Printf("User not found for sub=%s email=%s: %v", req.Sub, req.Email, err)
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
	}

	// Step 2: Get user's team memberships
	memberships, err := h.repo.GetUserTeamMemberships(ctx, user.ID)
	if err != nil {
		log.Printf("Failed to get team memberships for user %s: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get team memberships"})
		return
	}

	if len(memberships) == 0 {
		log.Printf("User %s has no team memberships", user.ID)
		c.JSON(http.StatusForbidden, gin.H{"error": "User has no team access"})
		return
	}

	// For now, use the first team membership (primary team)
	// TODO: Enhance to handle multiple teams based on request context
	primaryMembership := memberships[0]

	// Step 3: Get team information
	team, err := h.repo.GetTeamByID(ctx, primaryMembership.TeamID)
	if err != nil {
		log.Printf("Failed to get team %s: %v", primaryMembership.TeamID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get team information"})
		return
	}

	// Step 4: Get user's model access (team-wide + user-specific)
	models, err := h.repo.GetUserModelAccess(ctx, user.ID, team.ID)
	if err != nil {
		log.Printf("Failed to get model access for user %s in team %s: %v", user.ID, team.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get model access"})
		return
	}

	// Step 5: Determine plan from team's default policy
	plan := "free" // default
	if team.DefaultPolicyID != nil {
		policy, err := h.repo.GetPolicyByID(ctx, *team.DefaultPolicyID)
		if err == nil {
			// Extract plan from policy name or kind
			plan = h.extractPlanFromPolicy(policy)
		}
	}

	// Step 6: Build response
	response := db.IdentityLookupResponse{
		UserID:        user.ID,
		TeamID:        team.ID,
		Plan:          plan,
		Groups:        h.buildGroups(team, primaryMembership, plan),
		ModelsAllowed: h.buildModelsAllowed(models),
	}

	log.Printf("Identity lookup successful for user %s (team: %s, plan: %s, models: %d)", 
		user.ID, team.ExtID, plan, len(models))

	c.JSON(http.StatusOK, response)
}

// extractPlanFromPolicy extracts plan name from policy
func (h *IdentityHandler) extractPlanFromPolicy(policy *db.Policy) string {
	// Extract plan from policy name (e.g., "free-5-2min" -> "free")
	name := strings.ToLower(policy.Name)
	if strings.Contains(name, "enterprise") {
		return "enterprise"
	} else if strings.Contains(name, "premium") {
		return "premium"
	}
	return "free"
}

// buildGroups builds the groups array for rate limiting descriptors
func (h *IdentityHandler) buildGroups(team *db.Team, membership db.TeamMembership, plan string) []string {
	groups := []string{
		fmt.Sprintf("team:%s", team.ExtID),
		fmt.Sprintf("plan:%s", plan),
		fmt.Sprintf("role:%s", membership.Role),
	}
	return groups
}

// buildModelsAllowed builds the list of model names the user can access
func (h *IdentityHandler) buildModelsAllowed(models []db.Model) []string {
	var modelNames []string
	for _, model := range models {
		modelNames = append(modelNames, model.Name)
	}
	return modelNames
}

// ResolveUser handles GET /users/resolve?principal=<username|email|sub>
// Resolves a username, email, or Keycloak sub to a user UUID
func (h *IdentityHandler) ResolveUser(c *gin.Context) {
	principal := c.Query("principal")
	if principal == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "principal parameter is required"})
		return
	}

	ctx := context.Background()
	
	// Try to find user by different methods
	var user *db.User
	var err error

	// First try as Keycloak ID (UUID format)
	user, err = h.repo.FindUserByKeycloakID(ctx, principal)
	if err != nil {
		// Try as email
		user, err = h.repo.FindUserByEmail(ctx, principal)
		if err != nil {
			// Try as username (check if there's a username field or use email lookup)
			// For now, assume username matches email prefix
			if !strings.Contains(principal, "@") {
				emailGuess := principal + "@example.com"
				user, err = h.repo.FindUserByEmail(ctx, emailGuess)
			}
			if err != nil {
				log.Printf("User not found for principal=%s: %v", principal, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
				return
			}
		}
	}

	// Return user information
	response := gin.H{
		"id":              user.ID,
		"email":           user.Email,
		"keycloak_user_id": user.KeycloakUserID,
		"display_name":    user.DisplayName,
		"type":           user.Type,
	}

	log.Printf("User resolved: principal=%s -> user_id=%s", principal, user.ID)
	c.JSON(http.StatusOK, response)
}
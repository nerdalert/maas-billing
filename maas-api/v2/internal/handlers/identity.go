package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/db"
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

// Profile bootstraps the authenticated user into the database and default team, then returns user info.
// GET /profile
func (h *IdentityHandler) Profile(c *gin.Context) {
	ctx := context.Background()

	// Extract identity from Authorino-injected headers (set into Gin context by middleware)
	keycloakUserID := c.GetString("user_id")
	email := c.GetString("user_email")

	if keycloakUserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	// Find or create user
	user, err := h.repo.FindUserByKeycloakID(ctx, keycloakUserID)
	if err != nil {
		// Create if not found; use email as display name when available
		displayName := email
		if displayName == "" {
			displayName = keycloakUserID
		}
		user, err = h.repo.CreateUser(ctx, keycloakUserID, email, displayName)
		if err != nil {
			log.Printf("Profile: failed to create user: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
			return
		}
	}

	// Ensure membership in the default team, if the team exists
	var defaultTeamExtID = "default"
	defaultTeam, teamErr := h.repo.GetTeamByExtID(ctx, defaultTeamExtID)
	if teamErr == nil {
		// Check membership; add if missing
		isMember, memErr := h.repo.IsTeamMember(defaultTeam.ID.String(), user.ID.String())
		if memErr != nil {
			log.Printf("Profile: failed to check team membership: %v", memErr)
		} else if !isMember {
			if addErr := h.repo.AddUserToTeam(ctx, user.ID, defaultTeam.ID, "member"); addErr != nil {
				// Non-fatal: membership might already exist due to race; log and continue
				log.Printf("Profile: failed to add user to default team: %v", addErr)
			}
		}
	} else {
		// Default team might not be created yet; continue without failing
		log.Printf("Profile: default team not found: %v", teamErr)
	}

	// Return user profile information
	c.JSON(http.StatusOK, gin.H{
		"id":               user.ID,
		"email":            user.Email,
		"keycloak_user_id": user.KeycloakUserID,
		"display_name":     user.DisplayName,
		"type":             user.Type,
	})
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

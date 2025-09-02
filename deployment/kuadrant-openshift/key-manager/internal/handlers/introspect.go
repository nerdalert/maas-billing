package handlers

import (
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// IntrospectRequest represents the OAuth2 introspection request
type IntrospectRequest struct {
	Token         string `json:"token" binding:"required"`
	RunAsUserID   string `json:"run_as_user_id,omitempty"`
}

// IntrospectResponse represents the OAuth2 introspection response
type IntrospectResponse struct {
	Active       bool     `json:"active"`
	APIKeyID     string   `json:"api_key_id,omitempty"`
	TeamID       string   `json:"team_id,omitempty"`
	UserID       string   `json:"user_id,omitempty"`
	Group        string   `json:"group,omitempty"`
	ModelsAllowed []string `json:"models_allowed,omitempty"`
	PolicyID     string   `json:"policy_id,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// IntrospectHandler handles API key introspection for Authorino
func (h *IdentityHandler) Introspect(c *gin.Context) {
	log.Printf("DEBUG: Introspect request received from %s", c.ClientIP())
	log.Printf("DEBUG: Request headers: %+v", c.Request.Header)
	log.Printf("DEBUG: Content-Type: %s", c.GetHeader("Content-Type"))
	
	// Read raw body for debugging
	bodyBytes, _ := io.ReadAll(c.Request.Body)
	log.Printf("DEBUG: Raw body (%d bytes): %s", len(bodyBytes), string(bodyBytes))
	
	// Reset body for further processing
	c.Request.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	
	var req IntrospectRequest
	
	// OAuth2 introspection typically uses form data, but try JSON first then form
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("DEBUG: JSON binding failed: %v, trying form data", err)
		
		// Parse form data manually from the raw body
		formValues, err := url.ParseQuery(string(bodyBytes))
		if err != nil {
			log.Printf("DEBUG: Failed to parse form query: %v", err)
			c.JSON(http.StatusBadRequest, IntrospectResponse{
				Active: false,
				Error:  "invalid_request",
			})
			return
		}
		
		req.Token = formValues.Get("token")
		req.RunAsUserID = formValues.Get("run_as_user_id")
		
		log.Printf("DEBUG: Form data - token: %s, run_as_user_id: %s", req.Token, req.RunAsUserID)
		log.Printf("DEBUG: All form values: %+v", formValues)
		
		if req.Token == "" {
			log.Printf("DEBUG: No token found in JSON or form data")
			c.JSON(http.StatusBadRequest, IntrospectResponse{
				Active: false,
				Error:  "invalid_request",
			})
			return
		}
	} else {
		log.Printf("DEBUG: JSON binding successful - token: %s, run_as_user_id: %s", req.Token, req.RunAsUserID)
	}

	// Extract key prefix (first 8 characters)
	log.Printf("DEBUG: Processing token: %s (length: %d)", req.Token, len(req.Token))
	if len(req.Token) < 8 {
		log.Printf("DEBUG: Token too short, rejecting")
		c.JSON(http.StatusUnauthorized, IntrospectResponse{
			Active: false,
			Error:  "invalid_key",
		})
		return
	}

	keyPrefix := req.Token[:8]
	log.Printf("DEBUG: Looking up key with prefix: %s", keyPrefix)
	
	// Look up API key by prefix
	apiKey, err := h.repo.GetAPIKeyByPrefix(keyPrefix)
	if err != nil {
		log.Printf("DEBUG: Failed to find API key by prefix: %v", err)
		c.JSON(http.StatusUnauthorized, IntrospectResponse{
			Active: false,
			Error:  "invalid_key",
		})
		return
	}

	log.Printf("DEBUG: Found API key: ID=%s, prefix=%s, hash=%s", apiKey.ID, apiKey.KeyPrefix, apiKey.KeyHash)

	// Verify the full key hash
	if !h.verifyAPIKey(req.Token, apiKey.KeyHash, apiKey.Salt) {
		log.Printf("DEBUG: Key verification failed")
		c.JSON(http.StatusUnauthorized, IntrospectResponse{
			Active: false,
			Error:  "invalid_key",
		})
		return
	}

	log.Printf("DEBUG: Key verification successful")

	// Handle Run-As logic for team service keys
	effectiveUserID := apiKey.UserID
	var userIDStr string
	if apiKey.UserID != nil {
		userIDStr = *apiKey.UserID
	}
	log.Printf("DEBUG: API key UserID: %s, TeamID: %s", userIDStr, apiKey.TeamID)
	if apiKey.UserID == nil || (apiKey.UserID != nil && *apiKey.UserID == "") {
		// This is a team service key - Run-As is required
		if req.RunAsUserID == "" {
			c.JSON(http.StatusForbidden, IntrospectResponse{
				Active: false,
				Error:  "run_as_required",
			})
			return
		}

		// Validate Run-As user is a member of the team
		isMember, err := h.repo.IsTeamMember(apiKey.TeamID, req.RunAsUserID)
		if err != nil || !isMember {
			c.JSON(http.StatusForbidden, IntrospectResponse{
				Active: false,
				Error:  "run_as_not_member",
			})
			return
		}

		effectiveUserID = &req.RunAsUserID
	}

	// Get team details for plan
	log.Printf("DEBUG: Looking up team: %s", apiKey.TeamID)
	team, err := h.repo.GetTeam(apiKey.TeamID)
	if err != nil {
		log.Printf("DEBUG: Failed to get team: %v", err)
		c.JSON(http.StatusUnauthorized, IntrospectResponse{
			Active: false,
			Error:  "invalid_key",
		})
		return
	}
	log.Printf("DEBUG: Found team: %s, DefaultPolicyID: %v", team.Name, team.DefaultPolicyID)

	// Get policy for plan - temporary fix: use DefaultPolicyID instead of PolicyID
	var policyID string
	if team.DefaultPolicyID != nil {
		policyID = team.DefaultPolicyID.String()
	} else {
		log.Printf("DEBUG: Team has no default policy ID")
		c.JSON(http.StatusUnauthorized, IntrospectResponse{
			Active: false,
			Error:  "invalid_key",
		})
		return
	}
	
	log.Printf("DEBUG: Looking up policy: %s", policyID)
	policy, err := h.repo.GetPolicy(policyID)
	if err != nil {
		log.Printf("DEBUG: Failed to get policy: %v", err)
		c.JSON(http.StatusUnauthorized, IntrospectResponse{
			Active: false,
			Error:  "invalid_key",
		})
		return
	}
	log.Printf("DEBUG: Found policy: %s", policy.Name)

	// Get models allowed (team grants + user-specific grants)
	var effectiveUserIDStr string
	if effectiveUserID != nil {
		effectiveUserIDStr = *effectiveUserID
	}
	log.Printf("DEBUG: Getting models for user: %s, team: %s", effectiveUserIDStr, apiKey.TeamID)
	modelsAllowed, err := h.repo.GetUserModelsAllowed(effectiveUserIDStr, apiKey.TeamID)
	if err != nil {
		log.Printf("DEBUG: Failed to get user models: %v", err)
		c.JSON(http.StatusInternalServerError, IntrospectResponse{
			Active: false,
			Error:  "internal_error",
		})
		return
	}
	log.Printf("DEBUG: Found %d models allowed: %v", len(modelsAllowed), modelsAllowed)

	// Return successful introspection
	response := IntrospectResponse{
		Active:        true,
		APIKeyID:      apiKey.ID,
		TeamID:        apiKey.TeamID,
		UserID:        effectiveUserIDStr,
		Group:         "plan:" + policy.Name, // Format as plan:name for rate limiting
		ModelsAllowed: modelsAllowed,
		PolicyID:      policyID, // Use the resolved policy ID
	}
	log.Printf("DEBUG: Returning successful introspection: %+v", response)
	c.JSON(http.StatusOK, response)
}

// verifyAPIKey verifies the provided key against stored hash and salt
func (h *IdentityHandler) verifyAPIKey(providedKey, storedHash, salt string) bool {
	log.Printf("DEBUG: Verifying key - provided: %s, stored: %s, salt: %s", providedKey, storedHash, salt)
	
	// For simple hashing (development/testing), the stored hash contains the original key
	// The stored hash comes from PostgreSQL bytea as hex-encoded bytes
	// Decode the hex string to get the original key
	if strings.HasPrefix(storedHash, "\\x") {
		log.Printf("DEBUG: Stored hash has \\x prefix, decoding hex")
		// Remove \x prefix and decode hex
		hexStr := storedHash[2:]
		decoded, err := hex.DecodeString(hexStr)
		if err != nil {
			log.Printf("DEBUG: Hex decode failed: %v", err)
			return false
		}
		// Remove null padding and compare
		originalKey := strings.TrimRight(string(decoded), "\x00")
		log.Printf("DEBUG: Decoded key: %s", originalKey)
		result := originalKey == providedKey
		log.Printf("DEBUG: Verification result: %t", result)
		return result
	}
	
	// Fallback to direct comparison
	log.Printf("DEBUG: Using direct comparison")
	result := storedHash == providedKey
	log.Printf("DEBUG: Direct comparison result: %t", result)
	return result
}
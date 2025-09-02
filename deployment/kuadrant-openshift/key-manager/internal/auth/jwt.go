package auth

import (
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"net/http"
)

// JWTAuthMiddleware extracts JWT user context from Authorino headers
func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		log.Printf("JWT Auth: Processing request to %s", c.Request.URL.Path)
		
		// Extract user information from Authorino-injected headers
		userID := c.GetHeader("X-MaaS-User-ID")
		userEmail := c.GetHeader("X-MaaS-User-Email")
		userRoles := c.GetHeader("X-MaaS-User-Roles")
		
		log.Printf("JWT Auth: UserID=%s, Email=%s, Roles=%s", userID, userEmail, userRoles)
		
		// Verify user is authenticated
		if userID == "" {
			log.Printf("JWT Auth: No user ID found in headers")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}
		
		// Parse roles from JSON array format
		roles := []string{}
		if userRoles != "" {
			// Handle both comma-separated and JSON array formats
			if strings.HasPrefix(userRoles, "[") && strings.HasSuffix(userRoles, "]") {
				// JSON array format: [role1,role2] -> remove brackets and split
				rolesStr := strings.Trim(userRoles, "[]")
				if rolesStr != "" {
					for _, role := range strings.Split(rolesStr, ",") {
						roles = append(roles, strings.TrimSpace(role))
					}
				}
			} else {
				// Comma-separated format
				roles = strings.Split(userRoles, ",")
			}
		}
		
		// Store user context in gin context
		c.Set("user_id", userID)
		c.Set("user_email", userEmail)
		c.Set("user_roles", roles)
		
		log.Printf("JWT Auth: User context set successfully for %s", userID)
		c.Next()
	}
}

// AdminRequiredMiddleware checks if user has admin role
func AdminRequiredMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userRoles, exists := c.Get("user_roles")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"error": "No role information available"})
			c.Abort()
			return
		}
		
		roles, ok := userRoles.([]string)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid role format"})
			c.Abort()
			return
		}
		
		// Check if user has admin role
		hasAdminRole := false
		for _, role := range roles {
			if role == "maas-admin" {
				hasAdminRole = true
				break
			}
		}
		
		if !hasAdminRole {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin role required"})
			c.Abort()
			return
		}
		
		c.Next()
	}
}

// UserContextMiddleware allows both admin and user access
func UserContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userRoles, exists := c.Get("user_roles")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"error": "No role information available"})
			c.Abort()
			return
		}
		
		roles, ok := userRoles.([]string)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid role format"})
			c.Abort()
			return
		}
		
		// Check if user has either admin or user role
		hasValidRole := false
		for _, role := range roles {
			if role == "maas-admin" || role == "maas-user" {
				hasValidRole = true
				break
			}
		}
		
		if !hasValidRole {
			c.JSON(http.StatusForbidden, gin.H{"error": "Valid role required (maas-admin or maas-user)"})
			c.Abort()
			return
		}
		
		c.Next()
	}
}
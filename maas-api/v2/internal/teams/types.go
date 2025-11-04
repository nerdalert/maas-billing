package teams

import "regexp"

// Team management structures
type CreateTeamRequest struct {
	TeamID        string `json:"team_id" binding:"required"`
	TeamName      string `json:"team_name" binding:"required"`
	Description   string `json:"description"`
	RateLimit     int    `json:"rate_limit,omitempty"`     // Rate limit per window (default: 100)
	RateWindow    string `json:"rate_window,omitempty"`    // Rate window (default: "1m")
	RateLimitSpec string `json:"rate_limit_spec,omitempty"` // JSONB rate limit specification
}

type UpdateTeamRequest struct {
	TeamName      *string `json:"team_name,omitempty"`
	Description   *string `json:"description,omitempty"`
	RateLimit     *int    `json:"rate_limit,omitempty"`
	RateWindow    *string `json:"rate_window,omitempty"`
	RateLimitSpec *string `json:"rate_limit_spec,omitempty"`
}

type CreateTeamResponse struct {
	TeamID        string `json:"team_id"`
	TeamName      string `json:"team_name"`
	Description   string `json:"description"`
	RateLimit     int    `json:"rate_limit"`
	RateWindow    string `json:"rate_window"`
	RateLimitSpec string `json:"rate_limit_spec"`
	CreatedAt     string `json:"created_at"`
}

type GetTeamResponse struct {
	TeamID        string       `json:"team_id"`
	TeamName      string       `json:"team_name"`
	Description   string       `json:"description"`
	RateLimit     int          `json:"rate_limit"`
	RateWindow    string       `json:"rate_window"`
	RateLimitSpec string       `json:"rate_limit_spec"`
	Members       []TeamMember `json:"users"`
	Keys          []string     `json:"keys"`
	CreatedAt     string       `json:"created_at"`
}

type TeamMember struct {
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email"`
	Role      string `json:"role"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	JoinedAt  string `json:"joined_at"`
}

// User management structures
type AddUserToTeamRequest struct {
	UserEmail string `json:"user_email" binding:"required"`
	Role      string `json:"role" binding:"required"`
}

// Validation helpers

// isValidTeamID validates team ID according to Kubernetes RFC 1123 subdomain rules
func isValidTeamID(teamID string) bool {
	// Must be 1-63 characters long
	if len(teamID) == 0 || len(teamID) > 63 {
		return false
	}

	// Must contain only lowercase alphanumeric characters and hyphens
	// Must start and end with an alphanumeric character
	validPattern := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	return validPattern.MatchString(teamID)
}

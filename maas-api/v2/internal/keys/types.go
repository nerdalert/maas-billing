package keys

// API key structures
type CreateTeamKeyRequest struct {
	UserID string `json:"user_id" binding:"required"`
	Alias  string `json:"alias" binding:"required"`
}

type CreateTeamKeyResponse struct {
	ID      string `json:"id"`
	APIKey  string `json:"api_key"`
	KeyHash string `json:"key_hash"`
	TeamID  string `json:"team_id"`
	UserID  string `json:"user_id"`
	Alias   string `json:"alias"`
}

// Legacy structures (keep for backward compatibility)
type GenerateKeyRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

type DeleteKeyRequest struct {
	Key string `json:"key" binding:"required"`
}

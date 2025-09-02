package keys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/db"
	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/teams"
)

// Manager handles API key operations
type Manager struct {
	clientset    *kubernetes.Clientset
	keyNamespace string
	teamMgr      *teams.Manager
	repo         *db.Repository
}

// NewManager creates a new key manager
func NewManager(clientset *kubernetes.Clientset, keyNamespace string, teamMgr *teams.Manager, repo *db.Repository) *Manager {
	return &Manager{
		clientset:    clientset,
		keyNamespace: keyNamespace,
		teamMgr:      teamMgr,
		repo:         repo,
	}
}

// CreateTeamKey creates a new API key for a team member
func (m *Manager) CreateTeamKey(teamID string, req *CreateTeamKeyRequest) (*CreateTeamKeyResponse, error) {
	// teamID is now a UUID, validate team exists in database
	if m.repo == nil {
		return nil, fmt.Errorf("database repository not available")
	}

	// Look up team by UUID in database
	teamUUID, err := uuid.Parse(teamID)
	if err != nil {
		return nil, fmt.Errorf("invalid team ID format: %w", err)
	}

	team, err := m.repo.GetTeamByID(context.Background(), teamUUID)
	if err != nil {
		return nil, fmt.Errorf("team not found: %w", err)
	}

	// Get team policy from database
	var teamPolicy string
	if team.DefaultPolicyID != nil {
		policy, err := m.repo.GetPolicyByID(context.Background(), *team.DefaultPolicyID)
		if err != nil {
			log.Printf("Warning: Failed to get team policy: %v", err)
			teamPolicy = "unlimited-policy" // fallback
		} else {
			teamPolicy = policy.Name
		}
	} else {
		teamPolicy = "unlimited-policy" // default
	}

	// Build team member info - simplified for database approach
	userEmail := req.UserEmail
	if userEmail == "" {
		userEmail = fmt.Sprintf("%s@company.com", req.UserID)
	}

	teamMember := &teams.TeamMember{
		UserID:    req.UserID,
		UserEmail: userEmail,
		Role:      "member",
		TeamID:    team.ExtID, // Use external ID for compatibility
		TeamName:  team.Name,
		Policy:    teamPolicy,
	}

	// Generate API key
	apiKey, err := GenerateSecureToken(48)
	if err != nil {
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	// Store API key in database (primary source of truth)
	if m.repo == nil {
		return nil, fmt.Errorf("database repository not available")
	}

	keyPrefix := apiKey[:8] // First 8 characters as prefix
	salt := generateSalt()
	
	// For now, store plaintext API key for direct comparison (TODO: implement Argon2 later)
	dbAPIKey, err := m.repo.CreateAPIKey(context.Background(), keyPrefix, apiKey, salt, team.ExtID, req.UserID, req.Alias)
	if err != nil {
		return nil, fmt.Errorf("failed to store API key in database: %w", err)
	}
	log.Printf("✅ API key stored in database for OAuth2 introspection: team=%s, user=%s, key_id=%s", teamID, req.UserID, dbAPIKey.ID)

	// Get inherited policies
	inheritedPolicies := m.buildInheritedPolicies(teamMember)

	response := &CreateTeamKeyResponse{
		APIKey:            apiKey,
		UserID:            req.UserID,
		TeamID:            team.ExtID, // Return external team ID for API consistency
		KeyID:             dbAPIKey.ID,
		Policy:            teamMember.Policy,
		CreatedAt:         time.Now().Format(time.RFC3339),
		InheritedPolicies: inheritedPolicies,
	}

	return response, nil
}

// CreateLegacyKey creates a key using the legacy format (for backward compatibility)
func (m *Manager) CreateLegacyKey(req *GenerateKeyRequest) (*CreateTeamKeyResponse, error) {
	// Use default team for legacy endpoint
	teamID := "default"

	// Create team key request (internally use new team-scoped logic)
	createKeyReq := &CreateTeamKeyRequest{
		UserID:            req.UserID,
		Alias:             "legacy-key",
		Models:            []string{}, // Empty models = inherit team defaults
		InheritTeamLimits: true,
	}

	// Call CreateTeamKey which includes Authorino restart
	return m.CreateTeamKey(teamID, createKeyReq)
}

// DeleteKey deletes an API key by its value
func (m *Manager) DeleteKey(apiKey string) (string, error) {
	// Create SHA256 hash of the provided key
	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	keyHash := hex.EncodeToString(hasher.Sum(nil))

	// Find and delete secret by label selector (use truncated hash)
	labelSelector := fmt.Sprintf("maas/key-sha256=%s", keyHash[:32])

	secrets, err := m.clientset.CoreV1().Secrets(m.keyNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to find API key: %w", err)
	}

	if len(secrets.Items) == 0 {
		return "", fmt.Errorf("API key not found")
	}

	// Delete the secret
	secretName := secrets.Items[0].Name
	err = m.clientset.CoreV1().Secrets(m.keyNamespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to delete API key: %w", err)
	}

	return secretName, nil
}

// DeleteTeamKey deletes a specific team API key by name
func (m *Manager) DeleteTeamKey(keyName string) (string, string, error) {
	// Get key secret to validate it exists and get team info
	keySecret, err := m.clientset.CoreV1().Secrets(m.keyNamespace).Get(
		context.Background(), keyName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("API key not found: %w", err)
	}

	teamID := keySecret.Labels["maas/team-id"]
	if teamID == "" {
		return "", "", fmt.Errorf("API key is not associated with a team")
	}

	// Delete the key secret
	err = m.clientset.CoreV1().Secrets(m.keyNamespace).Delete(
		context.Background(), keyName, metav1.DeleteOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to delete API key: %w", err)
	}

	log.Printf("Team API key deleted successfully: %s from team %s", keyName, teamID)
	return keyName, teamID, nil
}

// ListTeamKeys lists all API keys for a team with details
func (m *Manager) ListTeamKeys(teamID string) ([]map[string]interface{}, error) {
	labelSelector := fmt.Sprintf("kuadrant.io/apikeys-by=rhcl-keys,maas/team-id=%s", teamID)
	secrets, err := m.clientset.CoreV1().Secrets(m.keyNamespace).List(
		context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}

	keys := make([]map[string]interface{}, 0)
	for _, secret := range secrets.Items {
		keyInfo := map[string]interface{}{
			"secret_name":    secret.Name,
			"user_id":        secret.Labels["maas/user-id"],
			"user_email":     secret.Annotations["maas/user-email"],
			"role":           secret.Labels["maas/team-role"],
			"policy":         secret.Annotations["maas/policy"],
			"models_allowed": secret.Annotations["maas/models-allowed"],
			"status":         secret.Annotations["maas/status"],
			"created_at":     secret.Annotations["maas/created-at"],
		}

		// Add alias if present
		if alias, exists := secret.Annotations["maas/alias"]; exists {
			keyInfo["alias"] = alias
		}

		// Add custom limits if present
		if customLimits, exists := secret.Annotations["maas/custom-limits"]; exists {
			var limits map[string]interface{}
			if err := json.Unmarshal([]byte(customLimits), &limits); err == nil {
				keyInfo["custom_limits"] = limits
			}
		}

		keys = append(keys, keyInfo)
	}

	return keys, nil
}

// ListUserKeys lists all API keys for a user across all teams
func (m *Manager) ListUserKeys(userID string) ([]map[string]interface{}, error) {
	labelSelector := fmt.Sprintf("kuadrant.io/apikeys-by=rhcl-keys,maas/user-id=%s", userID)
	secrets, err := m.clientset.CoreV1().Secrets(m.keyNamespace).List(
		context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}

	keys := make([]map[string]interface{}, 0)
	for _, secret := range secrets.Items {
		keyInfo := map[string]interface{}{
			"secret_name":    secret.Name,
			"team_id":        secret.Labels["maas/team-id"],
			"team_name":      secret.Annotations["maas/team-name"],
			"user_email":     secret.Annotations["maas/user-email"],
			"role":           secret.Labels["maas/team-role"],
			"policy":         secret.Annotations["maas/policy"],
			"models_allowed": secret.Annotations["maas/models-allowed"],
			"status":         secret.Annotations["maas/status"],
			"created_at":     secret.Annotations["maas/created-at"],
		}

		// Add alias if present
		if alias, exists := secret.Annotations["maas/alias"]; exists {
			keyInfo["alias"] = alias
		}

		// Add custom limits if present
		if customLimits, exists := secret.Annotations["maas/custom-limits"]; exists {
			var limits map[string]interface{}
			if err := json.Unmarshal([]byte(customLimits), &limits); err == nil {
				keyInfo["custom_limits"] = limits
			}
		}

		keys = append(keys, keyInfo)
	}

	return keys, nil
}

// validateTeamMembership validates team membership from existing API key
func (m *Manager) validateTeamMembership(teamID, userID string) (*teams.TeamMember, error) {
	// Look for any existing API key for this user in this team to validate membership
	labelSelector := fmt.Sprintf("kuadrant.io/apikeys-by=rhcl-keys,maas/team-id=%s,maas/user-id=%s", teamID, userID)
	secrets, err := m.clientset.CoreV1().Secrets(m.keyNamespace).List(
		context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, fmt.Errorf("failed to check user membership: %w", err)
	}

	if len(secrets.Items) == 0 {
		return nil, fmt.Errorf("user %s is not a member of team %s", userID, teamID)
	}

	// Extract membership info from existing API key secret
	secret := secrets.Items[0]
	member := &teams.TeamMember{
		UserID:    userID,
		TeamID:    teamID,
		UserEmail: secret.Annotations["maas/user-email"],
		Role:      secret.Labels["maas/team-role"],
		TeamName:  secret.Annotations["maas/team-name"],
		Policy:    secret.Annotations["maas/policy"],
		JoinedAt:  secret.Annotations["maas/created-at"],
	}

	return member, nil
}


// buildInheritedPolicies builds the inherited policies response
func (m *Manager) buildInheritedPolicies(teamMember *teams.TeamMember) map[string]interface{} {
	return map[string]interface{}{
		"policy":    teamMember.Policy,
		"team_id":   teamMember.TeamID,
		"team_name": teamMember.TeamName,
		"role":      teamMember.Role,
	}
}

// generateSalt generates a random salt for key hashing
func generateSalt() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// hashAPIKey hashes an API key with salt for database storage
func hashAPIKey(apiKey, salt string) string {
	h := sha256.New()
	h.Write([]byte(apiKey + salt))
	return hex.EncodeToString(h.Sum(nil))
}

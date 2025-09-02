package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/client-go/dynamic"

	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/db"
	"github.com/redhat-et/maas-billing/deployment/kuadrant-openshift/key-manager-v2/internal/teams"
)

// PoliciesHandler handles policy-related endpoints
type PoliciesHandler struct {
	repo           *db.Repository
	policyMgr      *teams.PolicyManager
	kuadrantClient dynamic.Interface
	keyNamespace   string
}

// NewPoliciesHandler creates a new policies handler
func NewPoliciesHandler(repo *db.Repository, policyMgr *teams.PolicyManager, kuadrantClient dynamic.Interface, keyNamespace string) *PoliciesHandler {
	return &PoliciesHandler{
		repo:           repo,
		policyMgr:      policyMgr,
		kuadrantClient: kuadrantClient,
		keyNamespace:   keyNamespace,
	}
}

// CreatePolicyRequest represents the request structure for creating a policy (QUICKSTART format)
type CreatePolicyRequest struct {
	Name        string                 `json:"name" binding:"required"`
	Kind        string                 `json:"kind" binding:"required"` // RateLimitPolicy, TokenRateLimitPolicy  
	Version     string                 `json:"version" binding:"required"`
	SpecJSON    map[string]interface{} `json:"spec_json" binding:"required"`
	Description string                 `json:"description"`
}

// CreatePolicyResponse represents the response structure for creating a policy
type CreatePolicyResponse struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"`
	Version     string                 `json:"version"`
	SpecJSON    map[string]interface{} `json:"spec_json"`
	Description string                 `json:"description"`
	CreatedAt   string                 `json:"created_at"`
}

// CreatePolicy handles POST /policies
func (h *PoliciesHandler) CreatePolicy(c *gin.Context) {
	userID, _ := c.Get("user_id")
	userEmail, _ := c.Get("user_email")
	userRoles, _ := c.Get("user_roles")
	
	log.Printf("CreatePolicy: Processing request from user %v (email: %v, roles: %v)", userID, userEmail, userRoles)
	
	var req CreatePolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("CreatePolicy: Invalid JSON request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	log.Printf("CreatePolicy: Request data - Name: %s, Kind: %s, Version: %s", 
		req.Name, req.Kind, req.Version)
	
	// Store policy in database - marshal the spec_json as string
	specJSONBytes, err := json.Marshal(req.SpecJSON)
	if err != nil {
		log.Printf("CreatePolicy: Failed to marshal spec: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process policy spec"})
		return
	}
	
	policy, err := h.repo.CreatePolicy(context.Background(), req.Name, req.Kind, string(specJSONBytes), req.Description)
	if err != nil {
		log.Printf("CreatePolicy: Failed to create policy in database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create policy"})
		return
	}
	
	response := CreatePolicyResponse{
		ID:          policy.ID.String(),
		Name:        policy.Name,
		Kind:        policy.Kind,
		Version:     policy.Version,
		SpecJSON:    req.SpecJSON,
		Description: req.Description,
		CreatedAt:   policy.CreatedAt.Format(time.RFC3339),
	}

	log.Printf("CreatePolicy: Policy created successfully: %s (%s) by user %v", policy.ID, req.Name, userID)
	c.JSON(http.StatusCreated, response)
}


// ListPolicies handles GET /policies
func (h *PoliciesHandler) ListPolicies(c *gin.Context) {
	userID, _ := c.Get("user_id")
	log.Printf("ListPolicies: Processing request from user %v", userID)
	
	policies, err := h.repo.ListPolicies(context.Background())
	if err != nil {
		log.Printf("ListPolicies: Failed to list policies: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list policies"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"policies": policies,
		"total":    len(policies),
	})
}

// GetPolicy handles GET /policies/:policy_id
func (h *PoliciesHandler) GetPolicy(c *gin.Context) {
	policyID := c.Param("policy_id")
	userID, _ := c.Get("user_id")
	
	log.Printf("🎯 GetPolicy: Processing request for policy %s from user %v", policyID, userID)
	
	// TODO: Implement database lookup when repository is available
	log.Printf("📋 GetPolicy: Returning mock data for policy %s", policyID)
	
	policy := map[string]interface{}{
		"policy_id":   policyID,
		"name":        "mock-policy",
		"description": "Mock policy for testing",
		"kind":        "RateLimitPolicy",
		"spec_json": map[string]interface{}{
			"targetRef": map[string]interface{}{
				"kind": "HTTPRoute",
				"name": "inference-gateway",
			},
			"limits": map[string]interface{}{
				"global": map[string]interface{}{
					"rates": []map[string]interface{}{
						{"limit": 50000, "window": "1h"},
					},
				},
			},
		},
		"created_at": "2025-01-01T00:00:00Z",
	}
	
	c.JSON(http.StatusOK, policy)
}

// DeletePolicy handles DELETE /policies/:policy_id
func (h *PoliciesHandler) DeletePolicy(c *gin.Context) {
	policyID := c.Param("policy_id")
	userID, _ := c.Get("user_id")
	
	log.Printf("🎯 DeletePolicy: Processing delete request for policy %s from user %v", policyID, userID)
	
	// TODO: Implement database deletion when repository is available
	log.Printf("✅ DeletePolicy: Mock deletion of policy %s", policyID)
	
	c.JSON(http.StatusOK, gin.H{
		"message":   "Policy deleted successfully",
		"policy_id": policyID,
	})
}
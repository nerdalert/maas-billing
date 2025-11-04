package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/metrics"
	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/types"
	"github.com/opendatahub-io/maas-billing/maas-api/v2/internal/usage"
)

// UsageHandler handles usage-related endpoints
type UsageHandler struct {
	clientset    *kubernetes.Clientset
	config       *rest.Config
	keyNamespace string
	collector    *usage.Collector
	promClient   *metrics.Client
	defaultRange string
	promDebug    bool
}

var usageRangePattern = regexp.MustCompile(`^[0-9]+(s|m|h|d|w|y)$`)

type namespaceUsageResponse struct {
	Namespace   string                 `json:"namespace"`
	Range       string                 `json:"range"`
	Metrics     map[string]metricUsage `json:"metrics"`
	GeneratedAt time.Time              `json:"generated_at"`
}

type metricUsage struct {
	Total        float64   `json:"total"`
	SampleCount  int       `json:"sample_count,omitempty"`
	LatestValue  float64   `json:"latest_value,omitempty"`
	LastSampleAt time.Time `json:"last_sample_at,omitempty"`
}

// NewUsageHandler creates a new usage handler
func NewUsageHandler(clientset *kubernetes.Clientset, config *rest.Config, keyNamespace string, promClient *metrics.Client, defaultRange string, promDebug bool) *UsageHandler {
	collector := usage.NewCollector(clientset, config, keyNamespace)

	return &UsageHandler{
		clientset:    clientset,
		config:       config,
		keyNamespace: keyNamespace,
		collector:    collector,
		promClient:   promClient,
		defaultRange: strings.TrimSpace(defaultRange),
		promDebug:    promDebug,
	}
}

// GetNamespaceUsage handles aggregated usage queries per Limitador namespace.
func (h *UsageHandler) GetNamespaceUsage(c *gin.Context) {
	if h.promClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Prometheus client is not configured"})
		return
	}

	namespace := strings.TrimSpace(c.Query("namespace"))
	if namespace == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "namespace query parameter is required"})
		return
	}

	requestedRange := strings.TrimSpace(c.Query("range"))
	if requestedRange == "" {
		requestedRange = h.defaultRange
	}
	if requestedRange == "" {
		requestedRange = "24h"
	}
	if !usageRangePattern.MatchString(requestedRange) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range must be a positive duration (e.g. 1m, 1h, 24h)"})
		return
	}

	ctx := c.Request.Context()
	metricsMap := make(map[string]metricUsage)
	metricNames := []string{"authorized_calls", "limited_calls", "authorized_hits"}

	for _, metricName := range metricNames {
		value, err := h.queryMetricIncrease(ctx, metricName, namespace, requestedRange)
		if err != nil {
			log.Printf("prometheus increase query failed for %s: %v", metricName, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to query metric %s: %v", metricName, err)})
			return
		}

		samples, err := h.queryMetricSeries(ctx, metricName, namespace, requestedRange)
		if err != nil {
			log.Printf("prometheus series query failed for %s: %v", metricName, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to query metric %s: %v", metricName, err)})
			return
		}

		if h.promDebug {
			log.Printf("usage debug: metric=%s namespace=%s total=%.3f samples=%d", metricName, namespace, value, len(samples))
		}

		mu := metricUsage{Total: value}
		if len(samples) > 0 {
			last := samples[len(samples)-1]
			mu.SampleCount = len(samples)
			mu.LatestValue = last.Value
			mu.LastSampleAt = last.Timestamp
		}

		metricsMap[metricName] = mu
	}

	response := namespaceUsageResponse{
		Namespace:   namespace,
		Range:       requestedRange,
		Metrics:     metricsMap,
		GeneratedAt: time.Now().UTC(),
	}

	c.JSON(http.StatusOK, response)
}

// GetUserUsage handles GET /users/:user_id/usage
func (h *UsageHandler) GetUserUsage(c *gin.Context) {
	userID := c.Param("user_id")

	// Collect usage data
	userUsage, err := h.collector.GetUserUsage(userID)
	if err != nil {
		log.Printf("Failed to get user usage for %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to collect usage data"})
		return
	}

	// Enrich with team names and user emails from secrets
	err = h.enrichUserUsage(userUsage)
	if err != nil {
		log.Printf("Failed to enrich user usage data: %v", err)
		// Continue with basic data even if enrichment fails
	}

	c.JSON(http.StatusOK, userUsage)
}

// GetTeamUsage handles GET /teams/:team_id/usage (admin only)
func (h *UsageHandler) GetTeamUsage(c *gin.Context) {
	teamID := c.Param("team_id")

	// Validate team exists
	teamSecret, err := h.clientset.CoreV1().Secrets(h.keyNamespace).Get(
		context.Background(), fmt.Sprintf("team-%s-config", teamID), metav1.GetOptions{})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	// Get team policy for metrics lookup
	policyName := teamSecret.Annotations["maas/policy"]
	if policyName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Team has no policy configured"})
		return
	}

	// Collect usage data
	teamUsage, err := h.collector.GetTeamUsage(teamID, policyName)
	if err != nil {
		log.Printf("Failed to get team usage for %s: %v", teamID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to collect usage data"})
		return
	}

	// Enrich with team metadata
	teamUsage.TeamName = teamSecret.Annotations["maas/team-name"]

	// Enrich with user emails from secrets
	err = h.enrichTeamUsage(teamUsage)
	if err != nil {
		log.Printf("Failed to enrich team usage data: %v", err)
		// Continue with basic data even if enrichment fails
	}

	c.JSON(http.StatusOK, teamUsage)
}

func (h *UsageHandler) queryMetricIncrease(ctx context.Context, metricName, namespace, rangeParam string) (float64, error) {
	labelValue := strconv.Quote(namespace)
	expr := fmt.Sprintf("increase(%s{limitador_namespace=%s}[%s])", metricName, labelValue, rangeParam)

	resp, err := h.promClient.Query(ctx, expr)
	if err != nil {
		return 0, err
	}

	if h.promDebug {
		log.Printf("usage debug: increase expr=%s resultType=%s series=%d", expr, resp.Data.ResultType, len(resp.Data.Result))
	}

	entry := selectSeriesEntry(resp.Data.Result, namespace)
	if entry == nil {
		return 0, nil
	}

	return metrics.ExtractVectorValue(*entry)
}

func (h *UsageHandler) queryMetricSeries(ctx context.Context, metricName, namespace, rangeParam string) ([]metrics.Sample, error) {
	labelValue := strconv.Quote(namespace)
	expr := fmt.Sprintf("%s{limitador_namespace=%s}[%s]", metricName, labelValue, rangeParam)

	resp, err := h.promClient.Query(ctx, expr)
	if err != nil {
		return nil, err
	}

	if h.promDebug {
		log.Printf("usage debug: series expr=%s resultType=%s series=%d", expr, resp.Data.ResultType, len(resp.Data.Result))
	}

	entry := selectSeriesEntry(resp.Data.Result, namespace)
	if entry == nil || len(entry.Values) == 0 {
		return nil, nil
	}

	return metrics.ExtractSamples(*entry)
}

func selectSeriesEntry(entries []metrics.SeriesEntry, namespace string) *metrics.SeriesEntry {
	for i := range entries {
		if entries[i].Metric["limitador_namespace"] == namespace {
			return &entries[i]
		}
	}
	if len(entries) == 1 {
		return &entries[0]
	}
	return nil
}

// enrichUserUsage adds team names and other metadata to user usage
func (h *UsageHandler) enrichUserUsage(userUsage *types.UserUsage) error {
	// Get all team config secrets to map policies to teams
	labelSelector := "maas/resource-type=team-config"
	secrets, err := h.clientset.CoreV1().Secrets(h.keyNamespace).List(
		context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return fmt.Errorf("failed to list team configs: %w", err)
	}

	// Create policy -> team mapping
	policyToTeam := make(map[string]struct {
		teamID   string
		teamName string
	})

	for _, secret := range secrets.Items {
		policy := secret.Annotations["maas/policy"]
		if policy != "" {
			policyToTeam[policy] = struct {
				teamID   string
				teamName string
			}{
				teamID:   secret.Labels["maas/team-id"],
				teamName: secret.Annotations["maas/team-name"],
			}
		}
	}

	// Enrich team breakdown with actual team info
	for i, teamUsage := range userUsage.TeamBreakdown {
		if teamInfo, exists := policyToTeam[teamUsage.Policy]; exists {
			userUsage.TeamBreakdown[i].TeamID = teamInfo.teamID
			userUsage.TeamBreakdown[i].TeamName = teamInfo.teamName
		}
	}

	return nil
}

// enrichTeamUsage adds user emails and other metadata to team usage
func (h *UsageHandler) enrichTeamUsage(teamUsage *types.TeamUsage) error {
	for i, userUsage := range teamUsage.UserBreakdown {
		// Find user's API key secret to get email
		labelSelector := fmt.Sprintf("kuadrant.io/apikeys-by=rhcl-keys,maas/team-id=%s,maas/user-id=%s",
			teamUsage.TeamID, userUsage.UserID)

		secrets, err := h.clientset.CoreV1().Secrets(h.keyNamespace).List(
			context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			log.Printf("Failed to get user secrets for %s: %v", userUsage.UserID, err)
			continue
		}

		if len(secrets.Items) > 0 {
			secret := secrets.Items[0]
			if email := secret.Annotations["maas/user-email"]; email != "" {
				teamUsage.UserBreakdown[i].UserEmail = email
			}
		}
	}

	return nil
}

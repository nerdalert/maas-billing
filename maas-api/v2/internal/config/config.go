package config

import (
	"os"
	"time"
)

// Config holds application configuration
type Config struct {
	// Server configuration
	Port        string
	ServiceName string

	// Kubernetes configuration
	KeyNamespace        string
	SecretSelectorLabel string
	SecretSelectorValue string

	// Kuadrant configuration
	TokenRateLimitPolicyName string

	// Default team configuration
	CreateDefaultTeam bool
	AdminAPIKey       string

	// Prometheus usage metrics configuration
	PrometheusURL         string
	PrometheusTokenPath   string
	PrometheusCAPath      string
	PrometheusInsecureTLS bool
	PrometheusTimeout     time.Duration
	UsageDefaultRange     string
	PrometheusDebug       bool
}

// Load loads configuration from environment variables
func Load() *Config {
	cfg := &Config{
		// Server configuration
		Port:        getEnvOrDefault("PORT", "8080"),
		ServiceName: getEnvOrDefault("SERVICE_NAME", "key-manager"),

		// Kubernetes configuration
		KeyNamespace:        getEnvOrDefault("KEY_NAMESPACE", "llm"),
		SecretSelectorLabel: getEnvOrDefault("SECRET_SELECTOR_LABEL", "kuadrant.io/apikeys-by"),
		SecretSelectorValue: getEnvOrDefault("SECRET_SELECTOR_VALUE", "rhcl-keys"),

		// Kuadrant configuration
		TokenRateLimitPolicyName: getEnvOrDefault("TOKEN_RATE_LIMIT_POLICY_NAME", "gateway-token-rate-limits"),

		// Default team configuration
		CreateDefaultTeam: getEnvOrDefault("CREATE_DEFAULT_TEAM", "true") == "true",
		AdminAPIKey:       getEnvOrDefault("ADMIN_API_KEY", ""),

		// Prometheus configuration
		PrometheusURL:         getEnvOrDefault("PROMETHEUS_URL", "https://prometheus-k8s.openshift-monitoring.svc.cluster.local:9091"),
		PrometheusTokenPath:   getEnvOrDefault("PROMETHEUS_TOKEN_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token"),
		PrometheusCAPath:      getEnvOrDefault("PROMETHEUS_CA_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"),
		PrometheusInsecureTLS: getEnvOrDefault("PROMETHEUS_INSECURE_SKIP_VERIFY", "true") == "true",
		UsageDefaultRange:     getEnvOrDefault("USAGE_DEFAULT_RANGE", "24h"),
		PrometheusDebug:       getEnvOrDefault("PROMETHEUS_DEBUG", "false") == "true",
	}

	if timeout, err := time.ParseDuration(getEnvOrDefault("PROMETHEUS_TIMEOUT", "10s")); err == nil {
		cfg.PrometheusTimeout = timeout
	} else {
		cfg.PrometheusTimeout = 10 * time.Second
	}

	return cfg
}

// getEnvOrDefault gets environment variable or returns default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

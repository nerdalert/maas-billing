package metrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// ClientConfig captures Prometheus connection options.
type ClientConfig struct {
	BaseURL            string
	TokenPath          string
	CAPath             string
	InsecureSkipVerify bool
	Timeout            time.Duration
}

// Client executes Prometheus queries using the in-cluster service account.
type Client struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewClient builds a Prometheus client from the provided configuration.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("prometheus base URL is required")
	}

	tokenPath := cfg.TokenPath
	if tokenPath == "" {
		tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read token: %w", err)
	}

	caPool, err := buildCAPool(cfg.CAPath)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	tlsConfig := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}
	if caPool != nil {
		tlsConfig.RootCAs = caPool
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   strings.TrimSpace(string(tokenBytes)),
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func buildCAPool(caPath string) (*x509.CertPool, error) {
	if caPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(caPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("failed to append CA certs from %s", path.Clean(caPath))
	}
	return pool, nil
}

// Query executes an instant query or range query (depending on expression) against Prometheus.
func (c *Client) Query(ctx context.Context, expr string) (*Response, error) {
	if c == nil {
		return nil, fmt.Errorf("prometheus client not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/query", nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Set("query", expr)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus responded %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed Response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if parsed.Status != "success" {
		if parsed.Error != "" {
			return nil, fmt.Errorf("prometheus error (%s): %s", parsed.ErrorType, parsed.Error)
		}
		return nil, fmt.Errorf("prometheus query failed")
	}

	return &parsed, nil
}

// Response models the subset of the Prometheus query response we care about.
type Response struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string        `json:"resultType"`
		Result     []SeriesEntry `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SeriesEntry represents a single series in the Prometheus response.
type SeriesEntry struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
	Values [][]interface{}   `json:"values"`
}

// Sample is a timestamped datapoint.
type Sample struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// ExtractVectorValue returns the numeric value from a vector response.
func ExtractVectorValue(entry SeriesEntry) (float64, error) {
	if len(entry.Value) != 2 {
		return 0, fmt.Errorf("unexpected vector format")
	}
	valueStr := fmt.Sprint(entry.Value[1])
	return strconv.ParseFloat(valueStr, 64)
}

// ExtractSamples converts a matrix response entry into samples.
func ExtractSamples(entry SeriesEntry) ([]Sample, error) {
	samples := make([]Sample, 0, len(entry.Values))
	for _, pair := range entry.Values {
		if len(pair) != 2 {
			continue
		}
		tsFloat, err := toFloat(pair[0])
		if err != nil {
			return nil, err
		}
		valFloat, err := toFloat(pair[1])
		if err != nil {
			return nil, err
		}
		sec := int64(tsFloat)
		nsec := int64((tsFloat - float64(sec)) * 1_000_000_000)
		samples = append(samples, Sample{
			Timestamp: time.Unix(sec, nsec).UTC(),
			Value:     valFloat,
		})
	}
	return samples, nil
}

func toFloat(v interface{}) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case string:
		return strconv.ParseFloat(t, 64)
	case json.Number:
		return t.Float64()
	default:
		return 0, fmt.Errorf("unsupported number type %T", v)
	}
}

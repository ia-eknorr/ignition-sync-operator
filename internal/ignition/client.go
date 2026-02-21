package ignition

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps Ignition gateway API calls.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient creates an Ignition API client.
// scheme should be "http" or "https", host is the gateway address (e.g., "localhost:8088").
func NewClient(scheme, host, apiKey string) *Client {
	return &Client{
		BaseURL: fmt.Sprintf("%s://%s", scheme, host),
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// ScanResult holds the outcome of scan API calls.
type ScanResult struct {
	ProjectsStatus int
	ConfigStatus   int
	Error          string
}

func (r ScanResult) String() string {
	if r.Error != "" {
		return fmt.Sprintf("error: %s", r.Error)
	}
	return fmt.Sprintf("projects=%d config=%d", r.ProjectsStatus, r.ConfigStatus)
}

// TriggerScan calls the Ignition scan APIs in order: projects first, then config.
// Returns a ScanResult. Errors are recorded but don't cause the agent to crash.
func (c *Client) TriggerScan() ScanResult {
	result := ScanResult{}

	// Scan projects first (order matters per architecture doc).
	status, err := c.postScan("/data/api/v1/scan/projects")
	if err != nil {
		result.Error = fmt.Sprintf("scan/projects: %v", err)
		return result
	}
	result.ProjectsStatus = status

	// Then scan config.
	status, err = c.postScan("/data/api/v1/scan/config")
	if err != nil {
		result.Error = fmt.Sprintf("scan/config: %v", err)
		return result
	}
	result.ConfigStatus = status

	return result
}

// postScan sends a POST to the scan endpoint with retries.
func (c *Client) postScan(path string) (int, error) {
	url := c.BaseURL + path

	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}

		req, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			return 0, fmt.Errorf("creating request: %w", err)
		}
		c.setAuth(req)

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.StatusCode, nil
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return 0, fmt.Errorf("after 3 retries: %w", lastErr)
}

// HealthCheck verifies the gateway is responsive.
// Returns nil if healthy, error if not reachable.
func (c *Client) HealthCheck() error {
	url := c.BaseURL + "/data/api/v1/gateway-info"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("gateway health check: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("gateway returned HTTP %d", resp.StatusCode)
}

// setAuth adds the Ignition API key header to a request.
func (c *Client) setAuth(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("X-Ignition-API-Token", strings.TrimSpace(c.APIKey))
	}
}

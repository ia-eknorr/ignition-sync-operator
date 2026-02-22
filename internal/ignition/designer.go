package ignition

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DesignerSession represents an active Ignition Designer session.
// Field names match the Ignition 8.3 REST API (GET /data/api/v1/designers).
type DesignerSession struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	Project  string `json:"project"`
	Address  string `json:"address"`
	Timezone string `json:"timezone"`
	Uptime   int64  `json:"uptime"`
}

// designerListResponse is the standard Ignition list envelope.
type designerListResponse struct {
	Items []DesignerSession `json:"items"`
}

// GetDesignerSessions queries the gateway for active Designer sessions.
// Returns an empty slice when no designers are connected.
func (c *Client) GetDesignerSessions(ctx context.Context) ([]DesignerSession, error) {
	url := c.BaseURL + "/data/api/v1/designers"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating designer sessions request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching designer sessions: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("designer sessions API returned HTTP %d", resp.StatusCode)
	}

	var envelope designerListResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decoding designer sessions response: %w", err)
	}

	if envelope.Items == nil {
		return []DesignerSession{}, nil
	}
	return envelope.Items, nil
}

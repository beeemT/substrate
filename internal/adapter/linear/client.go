package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxResponseBodyBytes limits HTTP response body reads to prevent OOM.
const maxResponseBodyBytes = 50 * 1024 * 1024 // 50 MiB

const defaultEndpoint = "https://api.linear.app/graphql"

type gqlClient struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

func newGQLClient(apiKey, endpoint string) *gqlClient {
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	return &gqlClient{
		apiKey:   strings.TrimSpace(apiKey),
		endpoint: endpoint,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// do executes a GraphQL request and unmarshals the response data into out.
// Returns an error for HTTP errors, GraphQL errors, or JSON decode failures.
// Respects context cancellation.
func (c *gqlClient) do(ctx context.Context, query string, variables map[string]any, out any) error {
	body := map[string]any{"query": query}
	if len(variables) > 0 {
		body["variables"] = variables
	}
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)
	resp, err := c.http.Do(req) //nolint:gosec // G704: URL constructed from trusted config, not user input
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	limitedBody := io.LimitReader(resp.Body, maxResponseBodyBytes)
	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(limitedBody)
		if len(respBody) > 0 {
			// Cap the body excerpt to keep log lines reasonable.
			detail := string(respBody)
			if len(detail) > 512 {
				detail = detail[:512] + "…"
			}
			return fmt.Errorf("linear API returned status %d: %s", resp.StatusCode, detail)
		}
		return fmt.Errorf("linear API returned status %d", resp.StatusCode)
	}
	var wrapper struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.NewDecoder(limitedBody).Decode(&wrapper); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(wrapper.Errors) > 0 {
		return fmt.Errorf("linear GraphQL error: %s", wrapper.Errors[0].Message)
	}
	if out != nil {
		if err := json.Unmarshal(wrapper.Data, out); err != nil {
			return fmt.Errorf("unmarshal data: %w", err)
		}
	}

	return nil
}

// ErrRateLimited is returned when Linear returns HTTP 429.
var ErrRateLimited = errors.New("linear: rate limited (429)")

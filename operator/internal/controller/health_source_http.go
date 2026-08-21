package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// healthzResponse is the operator's minimal decoding of GET /healthz's
// per-topic shape; see server/healthz.go in the root module for the full
// shape.
type healthzResponse struct {
	Topics map[string]healthzTopic `json:"topics"`
}

type healthzTopic struct {
	PartitionCount int32  `json:"partition_count"`
	CleanupPolicy  string `json:"cleanup_policy"`
}

// httpHealthSource reads a topic's shape by calling an rpkv process's
// /healthz endpoint over HTTP.
type httpHealthSource struct {
	client *http.Client
}

// newHTTPHealthSource wires an already-configured HTTP client into an
// httpHealthSource.
func newHTTPHealthSource(client *http.Client) *httpHealthSource {
	return &httpHealthSource{client: client}
}

// Shape fetches baseURL's /healthz and returns topic's reported shape.
func (s *httpHealthSource) Shape(ctx context.Context, baseURL, topic string) (topicShape, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return topicShape{}, fmt.Errorf("httpHealthSource: build request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return topicShape{}, fmt.Errorf("httpHealthSource: get %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return topicShape{}, fmt.Errorf("httpHealthSource: get %s: status %d", baseURL, resp.StatusCode)
	}

	var body healthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return topicShape{}, fmt.Errorf("httpHealthSource: decode %s: %w", baseURL, err)
	}

	t, ok := body.Topics[topic]
	if !ok {
		return topicShape{}, fmt.Errorf("httpHealthSource: topic %q not reported by %s", topic, baseURL)
	}

	return topicShape{CleanupPolicy: t.CleanupPolicy, PartitionCount: t.PartitionCount}, nil
}

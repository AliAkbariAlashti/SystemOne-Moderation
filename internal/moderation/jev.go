package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const endpoint = "https://api.typesafe.ai/v1/systemone"

type Client struct {
	apiKey   string
	http     *http.Client
	endpoint string
}

func NewClient(apiKey string) *Client {
	return &Client{apiKey: apiKey, endpoint: endpoint, http: &http.Client{Timeout: 30 * time.Second}}
}

type question struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}
type jevRequest struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]question `json:"questions"`
}
type answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        string             `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}
type jevResponse struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
}

func (c *Client) Evaluate(ctx context.Context, policy Policy, text string) (jevResponse, error) {
	questions := make(map[string]question, len(policy.Classes))
	for _, class := range policy.Classes {
		questions[class.ID] = question{Type: class.Type, Instructions: class.Definition, Criteria: class.Criteria}
	}
	body, err := json.Marshal(jevRequest{State: map[string]string{"text": text}, Model: policy.Model, Questions: questions})
	if err != nil {
		return jevResponse{}, fmt.Errorf("encode Jev request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return jevResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return jevResponse{}, fmt.Errorf("call Jev: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return jevResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return jevResponse{}, &UpstreamError{Status: resp.StatusCode}
	}
	var result jevResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return jevResponse{}, fmt.Errorf("decode Jev response: %w", err)
	}
	return result, nil
}

// UpstreamError deliberately excludes response bodies, which can contain private text.
type UpstreamError struct{ Status int }

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("moderation provider returned HTTP %d", e.Status)
}
func Retryable(err error) bool {
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		return false
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Status == 429 || upstream.Status == 408 || upstream.Status >= 500
	}
	return !errors.Is(err, context.Canceled)
}
func (a answer) validate(c Class) error {
	bad := func() error { return fmt.Errorf("invalid provider answer for class %q", c.ID) }
	if a.Type != "" && a.Type != c.Type {
		return bad()
	}
	switch c.Type {
	case "noul":
		if a.Noul == nil || !inRange(*a.Noul, 0, 1) {
			return bad()
		}
	case "choice":
		var criteria map[string]string
		_ = json.Unmarshal(c.Criteria, &criteria)
		if _, ok := criteria[a.Choice]; !ok {
			return bad()
		}
		if a.Confidence == nil || !inRange(*a.Confidence, 0, 1) {
			return bad()
		}
	case "score":
		var criteria []string
		_ = json.Unmarshal(c.Criteria, &criteria)
		if a.Score == nil || !inRange(*a.Score, 0, float64(len(criteria)-1)) || a.Confidence == nil || !inRange(*a.Confidence, 0, 1) {
			return bad()
		}
	}
	return nil
}

package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
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
		return jevResponse{}, &ProviderResponseError{}
	}
	return result, nil
}

// UpstreamError deliberately excludes response bodies, which can contain private text.
type UpstreamError struct{ Status int }

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("moderation provider returned HTTP %d", e.Status)
}

// ProviderResponseError means Jev returned a successful HTTP response that did
// not satisfy its answer contract. Retrying the same malformed response only
// spends provider capacity and delays dead-letter handling.
type ProviderResponseError struct{}

func (*ProviderResponseError) Error() string { return "invalid response from moderation provider" }

func Retryable(err error) bool {
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		return false
	}
	var response *ProviderResponseError
	if errors.As(err, &response) {
		return false
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Status == 429 || upstream.Status == 408 || upstream.Status >= 500
	}
	return !errors.Is(err, context.Canceled)
}
func (a answer) validate(c Class) error {
	bad := func() error { return &ProviderResponseError{} }
	if a.Type != c.Type {
		return bad()
	}
	switch c.Type {
	case "noul":
		if a.Noul == nil || !inRange(*a.Noul, 0, 1) {
			return bad()
		}
	case "choice":
		var criteria map[string]string
		if json.Unmarshal(c.Criteria, &criteria) != nil {
			return bad()
		}
		if _, ok := criteria[a.Choice]; !ok {
			return bad()
		}
		if a.Confidence == nil || !inRange(*a.Confidence, 0, 1) {
			return bad()
		}
		if !validDistribution(a.Probabilities, choiceKeys(criteria)) {
			return bad()
		}
	case "score":
		var criteria []string
		if json.Unmarshal(c.Criteria, &criteria) != nil {
			return bad()
		}
		if a.Score == nil || !inRange(*a.Score, 0, float64(len(criteria)-1)) || a.Confidence == nil || !inRange(*a.Confidence, 0, 1) {
			return bad()
		}
		keys := make([]string, len(criteria))
		for i := range criteria {
			keys[i] = strconv.Itoa(i)
		}
		if !validDistribution(a.Probabilities, keys) {
			return bad()
		}
	}
	return nil
}

func choiceKeys(criteria map[string]string) []string {
	keys := make([]string, 0, len(criteria))
	for key := range criteria {
		keys = append(keys, key)
	}
	return keys
}

func validDistribution(probabilities map[string]float64, expected []string) bool {
	if len(probabilities) != len(expected) {
		return false
	}
	var total float64
	for _, key := range expected {
		value, ok := probabilities[key]
		if !ok || !inRange(value, 0, 1) {
			return false
		}
		total += value
	}
	return math.Abs(total-1) <= 1e-6
}

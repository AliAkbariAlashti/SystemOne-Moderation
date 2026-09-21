package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const endpoint = "https://api.typesafe.ai/v1/systemone"

type Client struct { apiKey string; http *http.Client }

func NewClient(apiKey string) *Client { return &Client{apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}} }

type question struct { Type string `json:"type"`; Instructions string `json:"instructions"`; Criteria json.RawMessage `json:"criteria,omitempty"` }
type jevRequest struct { State any `json:"state"`; Model string `json:"model"`; Questions map[string]question `json:"questions"` }
type answer struct {
	Type string `json:"type"`; Noul float64 `json:"noul"`; Choice string `json:"choice"`; Score float64 `json:"score"`; Confidence float64 `json:"confidence"`; Probabilities map[string]float64 `json:"probabilities"`
}
type jevResponse struct { Model string `json:"model"`; Answers map[string]answer `json:"answers"` }

func (c *Client) Evaluate(ctx context.Context, policy Policy, text string) (jevResponse, error) {
	questions := make(map[string]question, len(policy.Classes))
	for _, class := range policy.Classes { questions[class.ID] = question{Type: class.Type, Instructions: class.Definition, Criteria: class.Criteria} }
	body, err := json.Marshal(jevRequest{State: map[string]string{"text": text}, Model: policy.Model, Questions: questions})
	if err != nil { return jevResponse{}, fmt.Errorf("encode Jev request: %w", err) }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil { return jevResponse{}, err }
	req.Header.Set("Authorization", "Bearer "+c.apiKey); req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil { return jevResponse{}, fmt.Errorf("call Jev: %w", err) }
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)); if err != nil { return jevResponse{}, err }
	if resp.StatusCode < 200 || resp.StatusCode > 299 { return jevResponse{}, fmt.Errorf("Jev returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody))) }
	var result jevResponse
	if err := json.Unmarshal(responseBody, &result); err != nil { return jevResponse{}, fmt.Errorf("decode Jev response: %w", err) }
	return result, nil
}

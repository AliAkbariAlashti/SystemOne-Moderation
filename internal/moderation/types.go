package moderation

import "encoding/json"

type Action string

const (
	Allow  Action = "allow"
	Review Action = "review"
	Block  Action = "block"
)

type Policy struct {
	Version           int      `json:"version"`
	Model             string   `json:"model"`
	DefaultAction     Action   `json:"default_action"`
	ActionsPrecedence []Action `json:"actions_precedence"`
	Classes           []Class  `json:"classes"`
}

type Class struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"` // noul, choice, or score
	Definition      string            `json:"definition"`
	Criteria        json.RawMessage   `json:"criteria"`
	ReviewThreshold *float64          `json:"review_threshold,omitempty"`
	ActionThreshold *float64          `json:"action_threshold,omitempty"`
	MinConfidence   *float64          `json:"min_confidence,omitempty"`
	ReviewScore     *float64          `json:"review_score,omitempty"`
	ActionScore     *float64          `json:"action_score,omitempty"`
	Action          Action            `json:"action"`
	ChoiceActions   map[string]Action `json:"choice_actions,omitempty"`
}

type Request struct {
	ID       string          `json:"id,omitempty"`
	Text     string          `json:"text"`
	Type     string          `json:"type"` // optional: noul, choice, or score; empty evaluates all
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type Finding struct {
	Reason        string             `json:"reason"`
	ClassID       string             `json:"class_id"`
	Type          string             `json:"type"`
	Action        Action             `json:"action"`
	Probability   *float64           `json:"probability,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type Result struct {
	PolicyVersion int             `json:"policy_version"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	ID            string          `json:"id,omitempty"`
	Action        Action          `json:"action"`
	Findings      []Finding       `json:"findings"`
	Model         string          `json:"model"`
}

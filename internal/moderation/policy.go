package moderation

import (
	"encoding/json"
	"fmt"
	"os"
)

func LoadPolicy(path string) (Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil { return Policy{}, fmt.Errorf("read policy: %w", err) }
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil { return Policy{}, fmt.Errorf("parse policy: %w", err) }
	if err := p.Validate(); err != nil { return Policy{}, err }
	return p, nil
}

func (p Policy) Validate() error {
	if p.Model == "" { return fmt.Errorf("policy model is required") }
	if len(p.Classes) == 0 { return fmt.Errorf("policy needs at least one class") }
	seen := map[string]bool{}
	for _, c := range p.Classes {
		if c.ID == "" || seen[c.ID] { return fmt.Errorf("class ids must be unique and non-empty") }
		seen[c.ID] = true
		if c.Definition == "" { return fmt.Errorf("class %q definition is required", c.ID) }
		switch c.Type {
		case "noul":
			if c.ReviewThreshold == nil || c.ActionThreshold == nil { return fmt.Errorf("noul class %q needs review_threshold and action_threshold", c.ID) }
		case "choice":
			if len(c.Criteria) == 0 { return fmt.Errorf("choice class %q needs criteria", c.ID) }
		case "score":
			if c.ReviewScore == nil || c.ActionScore == nil { return fmt.Errorf("score class %q needs review_score and action_score", c.ID) }
		default: return fmt.Errorf("class %q has unsupported type %q", c.ID, c.Type)
		}
	}
	return nil
}

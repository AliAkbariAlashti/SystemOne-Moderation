package moderation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
)

func LoadPolicy(path string) (Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read policy: %w", err)
	}
	var p Policy
	if err := strictJSON(b, &p); err != nil {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (p Policy) Validate() error {
	if p.Version < 1 {
		return fmt.Errorf("policy version must be positive")
	}
	if !validAction(p.DefaultAction) {
		return fmt.Errorf("invalid default_action")
	}
	actions := map[Action]bool{}
	for _, a := range p.ActionsPrecedence {
		if !validAction(a) || actions[a] {
			return fmt.Errorf("invalid actions_precedence")
		}
		actions[a] = true
	}
	if len(actions) != 3 {
		return fmt.Errorf("actions_precedence must contain block, review, allow exactly once")
	}
	if p.Model == "" {
		return fmt.Errorf("policy model is required")
	}
	if len(p.Classes) == 0 {
		return fmt.Errorf("policy needs at least one class")
	}
	seen := map[string]bool{}
	for _, c := range p.Classes {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("class ids must be unique and non-empty")
		}
		seen[c.ID] = true
		if c.Definition == "" {
			return fmt.Errorf("class %q definition is required", c.ID)
		}
		if !validAction(c.Action) {
			return fmt.Errorf("class %q has invalid action", c.ID)
		}
		if c.MinConfidence != nil && !inRange(*c.MinConfidence, 0, 1) {
			return fmt.Errorf("invalid min_confidence for %q", c.ID)
		}
		switch c.Type {
		case "noul":
			if len(c.Criteria) > 0 {
				var criteria map[string]string
				if err := json.Unmarshal(c.Criteria, &criteria); err != nil || len(criteria) != 2 || criteria["true"] == "" || criteria["false"] == "" {
					return fmt.Errorf("noul %q criteria must define true and false", c.ID)
				}
			}

			if c.ReviewThreshold == nil || c.ActionThreshold == nil {
				return fmt.Errorf("noul class %q needs review_threshold and action_threshold", c.ID)
			}
			if !ordered(c.ReviewThreshold, c.ActionThreshold, 1) {
				return fmt.Errorf("invalid thresholds for %q", c.ID)
			}
		case "choice":
			if len(c.Criteria) == 0 {
				return fmt.Errorf("choice class %q needs criteria", c.ID)
			}
			var criteria map[string]string
			if err := json.Unmarshal(c.Criteria, &criteria); err != nil || len(criteria) < 2 {
				return fmt.Errorf("choice %q needs at least two named criteria", c.ID)
			}
			for k, v := range criteria {
				if k == "" || v == "" {
					return fmt.Errorf("empty choice criterion")
				}
			}
			for k, a := range c.ChoiceActions {
				if _, ok := criteria[k]; !ok || !validAction(a) {
					return fmt.Errorf("invalid choice action for %q", c.ID)
				}
			}
		case "score":
			if c.ReviewScore == nil || c.ActionScore == nil {
				return fmt.Errorf("score class %q needs review_score and action_score", c.ID)
			}
			var criteria []string
			if err := json.Unmarshal(c.Criteria, &criteria); err != nil || len(criteria) < 2 {
				return fmt.Errorf("score %q needs ordered criteria", c.ID)
			}
			for _, v := range criteria {
				if v == "" {
					return fmt.Errorf("empty score criterion")
				}
			}
			if !ordered(c.ReviewScore, c.ActionScore, float64(len(criteria)-1)) {
				return fmt.Errorf("invalid score thresholds for %q", c.ID)
			}
		default:
			return fmt.Errorf("class %q has unsupported type %q", c.ID, c.Type)
		}
	}
	return nil
}

func validAction(a Action) bool { return a == Allow || a == Review || a == Block }
func inRange(v, low, high float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= low && v <= high
}
func ordered(low, high *float64, max float64) bool {
	return low != nil && high != nil && inRange(*low, 0, max) && inRange(*high, *low, max)
}
func strictJSON(b []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

package moderation

import (
	"context"
	"fmt"
	"strings"
)

type Service struct {
	client *Client
	policy Policy
}

func NewService(client *Client, policy Policy) *Service {
	return &Service{client: client, policy: policy}
}

func (s *Service) Moderate(ctx context.Context, input Request) (Result, error) {
	if err := input.Validate(); err != nil {
		return Result{}, err
	}

	policy := s.policy
	policy.Classes = nil
	for _, class := range s.policy.Classes {
		if input.Type == "" || class.Type == input.Type {
			policy.Classes = append(policy.Classes, class)
		}
	}
	if len(policy.Classes) == 0 {
		return Result{}, &ValidationError{Message: fmt.Sprintf("policy has no %s classes", input.Type)}
	}
	response, err := s.client.Evaluate(ctx, policy, input.Text)
	if err != nil {
		return Result{}, err
	}
	result := Result{ID: input.ID, Action: s.policy.DefaultAction, Model: response.Model, PolicyVersion: policy.Version, Metadata: input.Metadata}
	for _, class := range policy.Classes {
		answer, ok := response.Answers[class.ID]
		if !ok {
			return Result{}, &ProviderResponseError{}
		}
		if err := answer.validate(class); err != nil {
			return Result{}, err
		}
		finding := Finding{ClassID: class.ID, Type: class.Type, Action: Allow, Probabilities: answer.Probabilities}
		switch class.Type {
		case "noul":
			p := *answer.Noul
			finding.Probability = &p
			if p >= *class.ActionThreshold {
				finding.Action = class.Action
			} else if p >= *class.ReviewThreshold {
				finding.Action = Review
			}
		case "choice":
			finding.Choice = answer.Choice
			confidence := *answer.Confidence
			finding.Confidence = &confidence
			if action, exists := class.ChoiceActions[answer.Choice]; exists {
				finding.Action = action
			}
			if class.MinConfidence != nil && confidence < *class.MinConfidence {
				finding.Action = Review
				finding.Reason = "low_confidence"
			}
		case "score":
			score := *answer.Score
			confidence := *answer.Confidence
			finding.Score = &score
			finding.Confidence = &confidence
			if score >= *class.ActionScore {
				finding.Action = class.Action
			} else if score >= *class.ReviewScore {
				finding.Action = Review
			}
		}
		if finding.Reason == "" {
			finding.Reason = string(finding.Action) + "_by_policy"
		}
		result.Findings = append(result.Findings, finding)
		result.Action = strongest(result.Action, finding.Action, s.policy.ActionsPrecedence)
	}
	return result, nil
}

func strongest(current, candidate Action, precedence []Action) Action {
	for _, action := range precedence {
		if action == candidate {
			return candidate
		}
		if action == current {
			return current
		}
	}
	return current
}

const MaxTextBytes = 100000

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func (r Request) Validate() error {
	if strings.TrimSpace(r.Text) == "" {
		return &ValidationError{"text is required"}
	}
	if len(r.Text) > MaxTextBytes {
		return &ValidationError{"text exceeds 100000 bytes"}
	}
	if r.Type != "" && r.Type != "noul" && r.Type != "choice" && r.Type != "score" {
		return &ValidationError{"type must be noul, choice, or score when provided"}
	}
	return nil
}

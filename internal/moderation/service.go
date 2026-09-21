package moderation

import (
	"context"
	"fmt"
)

type Service struct { client *Client; policy Policy }
func NewService(client *Client, policy Policy) *Service { return &Service{client: client, policy: policy} }

func (s *Service) Moderate(ctx context.Context, input Request) (Result, error) {
	if input.Text == "" { return Result{}, fmt.Errorf("text is required") }
	if input.Type != "noul" && input.Type != "choice" && input.Type != "score" { return Result{}, fmt.Errorf("type must be one of: noul, choice, score") }
	policy := s.policy
	policy.Classes = nil
	for _, class := range s.policy.Classes { if class.Type == input.Type { policy.Classes = append(policy.Classes, class) } }
	if len(policy.Classes) == 0 { return Result{}, fmt.Errorf("policy has no %s classes", input.Type) }
	response, err := s.client.Evaluate(ctx, policy, input.Text); if err != nil { return Result{}, err }
	result := Result{ID: input.ID, Action: s.policy.DefaultAction, Model: response.Model}
	for _, class := range policy.Classes {
		answer, ok := response.Answers[class.ID]; if !ok { return Result{}, fmt.Errorf("Jev response omitted class %q", class.ID) }
		finding := Finding{ClassID: class.ID, Type: class.Type, Action: Allow, Probabilities: answer.Probabilities}
		switch class.Type {
		case "noul":
			p := answer.Noul; finding.Probability = &p
			if p >= *class.ActionThreshold { finding.Action = class.Action } else if p >= *class.ReviewThreshold { finding.Action = Review }
		case "choice":
			finding.Choice = answer.Choice; confidence := answer.Confidence; finding.Confidence = &confidence
			if class.MinConfidence != nil && confidence < *class.MinConfidence { finding.Action = Review }
			if action, exists := class.ChoiceActions[answer.Choice]; exists { finding.Action = action }
		case "score":
			score := answer.Score; confidence := answer.Confidence; finding.Score = &score; finding.Confidence = &confidence
			if score >= *class.ActionScore { finding.Action = class.Action } else if score >= *class.ReviewScore { finding.Action = Review }
		}
		result.Findings = append(result.Findings, finding)
		result.Action = strongest(result.Action, finding.Action, s.policy.ActionsPrecedence)
	}
	return result, nil
}

func strongest(current, candidate Action, precedence []Action) Action {
	for _, action := range precedence { if action == candidate { return candidate }; if action == current { return current } }
	return current
}

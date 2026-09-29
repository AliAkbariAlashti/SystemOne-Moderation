package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixture(t *testing.T) Policy {
	t.Helper()
	p, err := LoadPolicy("../../config/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, kind, answers string
		want                Action
		fail                bool
	}{
		{"low confidence safe", "choice", `{"content_class":{"type":"choice","choice":"safe","confidence":0.2,"probabilities":{"safe":1,"hate":0,"spam":0,"obscene":0,"unclear":0}}}`, Review, false},
		{"low confidence harmful", "choice", `{"content_class":{"type":"choice","choice":"hate","confidence":0.2,"probabilities":{"safe":0,"hate":1,"spam":0,"obscene":0,"unclear":0}}}`, Review, false},
		{"confident harmful", "choice", `{"content_class":{"type":"choice","choice":"hate","confidence":0.9,"probabilities":{"safe":0,"hate":1,"spam":0,"obscene":0,"unclear":0}}}`, Block, false},
		{"missing numeric value", "noul", `{"allowed":{},"blocked":{"noul":0.1}}`, "", true},
		{"unknown choice", "choice", `{"content_class":{"choice":"other","confidence":0.9}}`, "", true},
		{"out of range", "score", `{"moderation_severity":{"score":4,"confidence":0.9}}`, "", true},
		{"missing class", "choice", `{}`, "", true},
		{"all classes", "", `{"allowed":{"type":"noul","noul":0.9},"blocked":{"type":"noul","noul":0.1},"content_class":{"type":"choice","choice":"safe","confidence":0.9,"probabilities":{"safe":1,"hate":0,"spam":0,"obscene":0,"unclear":0}},"moderation_severity":{"type":"score","score":0,"confidence":0.9,"probabilities":{"0":1,"1":0,"2":0,"3":0}}}`, Allow, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req jevRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if tc.kind == "" && len(req.Questions) != len(p.Classes) {
					t.Error("default did not evaluate all classes")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"model":"test","answers":` + tc.answers + `}`))
			}))
			defer server.Close()
			client := NewClient("test")
			client.endpoint = server.URL
			result, err := NewService(client, p).Moderate(context.Background(), Request{ID: "item", Text: "hello", Type: tc.kind, Metadata: json.RawMessage(`{"app":"test"}`)})
			if tc.fail {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Action != tc.want {
				t.Fatalf("got %s, want %s", result.Action, tc.want)
			}
			if result.PolicyVersion != p.Version || string(result.Metadata) != `{"app":"test"}` {
				t.Fatal("lost audit context")
			}
		})
	}
}
func TestPolicyValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Policy)
	}{
		{"invalid default", func(p *Policy) { p.DefaultAction = "bad" }},
		{"missing precedence", func(p *Policy) { p.ActionsPrecedence = nil }},
		{"duplicate precedence", func(p *Policy) { p.ActionsPrecedence = []Action{Allow, Allow, Block} }},
		{"reversed thresholds", func(p *Policy) { v := 0.99; p.Classes[0].ReviewThreshold = &v }},
		{"invalid choice action", func(p *Policy) { p.Classes[2].ChoiceActions["unknown"] = Block }},
		{"missing score criteria", func(p *Policy) { p.Classes[3].Criteria = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture(t)
			tc.change(&p)
			if p.Validate() == nil {
				t.Fatal("accepted invalid policy")
			}
		})
	}
}
func TestRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{{&ValidationError{"bad"}, false}, {&ProviderResponseError{}, false}, {&UpstreamError{401}, false}, {&UpstreamError{429}, true}, {&UpstreamError{503}, true}, {context.Canceled, false}} {
		if Retryable(tc.err) != tc.want {
			t.Errorf("wrong retry classification: %v", tc.err)
		}
	}
}

func TestThresholdBoundaries(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  Action
	}{{0.419, Allow}, {0.42, Review}, {0.719, Review}, {0.72, Block}} {
		t.Run(fmt.Sprint(tc.value), func(t *testing.T) {
			p := fixture(t)
			p.Classes = p.Classes[1:2]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"answers":{"blocked":{"type":"noul","noul":%v}}}`, tc.value)
			}))
			defer server.Close()
			client := NewClient("test")
			client.endpoint = server.URL
			result, err := NewService(client, p).Moderate(context.Background(), Request{Text: "sample"})
			if err != nil || result.Action != tc.want {
				t.Fatalf("got %s, %v; want %s", result.Action, err, tc.want)
			}
		})
	}
}

func TestProviderResponseContract(t *testing.T) {
	p := fixture(t)
	choice := p.Classes[2]
	score := p.Classes[3]
	validChoice := answer{Type: "choice", Choice: "safe", Confidence: float64Ptr(0.9), Probabilities: map[string]float64{"safe": 1, "hate": 0, "spam": 0, "obscene": 0, "unclear": 0}}
	validScore := answer{Type: "score", Score: float64Ptr(0), Confidence: float64Ptr(0.9), Probabilities: map[string]float64{"0": 1, "1": 0, "2": 0, "3": 0}}
	for _, tc := range []struct {
		name   string
		answer answer
		class  Class
	}{
		{"missing type", answer{Noul: float64Ptr(0.9)}, p.Classes[0]},
		{"choice missing probability", answer{Type: "choice", Choice: "safe", Confidence: float64Ptr(0.9), Probabilities: map[string]float64{"safe": 1}}, choice},
		{"choice distribution does not sum", answer{Type: "choice", Choice: "safe", Confidence: float64Ptr(0.9), Probabilities: map[string]float64{"safe": 0.9, "hate": 0, "spam": 0, "obscene": 0, "unclear": 0}}, choice},
		{"score extra probability", answer{Type: "score", Score: float64Ptr(0), Confidence: float64Ptr(0.9), Probabilities: map[string]float64{"0": 1, "1": 0, "2": 0, "3": 0, "4": 0}}, score},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.answer.validate(tc.class)
			var response *ProviderResponseError
			if !errors.As(err, &response) {
				t.Fatalf("got %v, want ProviderResponseError", err)
			}
		})
	}
	if err := validChoice.validate(choice); err != nil {
		t.Fatalf("valid choice rejected: %v", err)
	}
	if err := validScore.validate(score); err != nil {
		t.Fatalf("valid score rejected: %v", err)
	}
}

func float64Ptr(value float64) *float64 { return &value }

func TestMalformedProviderResponseIsNotRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":`))
	}))
	defer server.Close()
	client := NewClient("test")
	client.endpoint = server.URL
	_, err := client.Evaluate(context.Background(), fixture(t), "sample")
	var response *ProviderResponseError
	if !errors.As(err, &response) || Retryable(err) {
		t.Fatalf("malformed response should be non-retryable: %v", err)
	}
}

func TestProviderErrorDoesNotExposeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "private submitted content")
	}))
	defer server.Close()
	client := NewClient("test")
	client.endpoint = server.URL
	_, err := client.Evaluate(context.Background(), fixture(t), "sample")
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Status != 401 || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe or missing provider error: %v", err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/example/jev-moderation-service/internal/moderation"
)

type edgeCase struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Text     string `json:"text"`
	Expected string `json:"expected"`
}

type row struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Expected string            `json:"expected"`
	Actual   string            `json:"actual"`
	Matched  bool              `json:"matched"`
	Action   moderation.Action `json:"action"`
}

func main() {
	casesPath := flag.String("cases", "testdata/edge_cases.json", "path to edge-case JSON")
	policyPath := flag.String("policy", "config/policy.json", "path to moderation policy")
	flag.Parse()
	apiKey := os.Getenv("TYPESAFE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "TYPESAFE_API_KEY is required")
		os.Exit(2)
	}
	b, err := os.ReadFile(*casesPath)
	if err != nil {
		panic(err)
	}
	var cases []edgeCase
	if err := json.Unmarshal(b, &cases); err != nil {
		panic(err)
	}
	policy, err := moderation.LoadPolicy(*policyPath)
	if err != nil {
		panic(err)
	}
	service := moderation.NewService(moderation.NewClient(apiKey), policy)
	matched := 0
	for _, test := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		result, err := service.Moderate(ctx, moderation.Request{ID: test.ID, Text: test.Text, Type: test.Type})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", test.ID, err)
			continue
		}
		actual := string(result.Action)
		if test.Type == "choice" {
			actual = result.Findings[0].Choice
		}
		if test.Type == "score" {
			actual = fmt.Sprintf("%.0f", *result.Findings[0].Score)
		}
		ok := actual == test.Expected
		if ok {
			matched++
		}
		_ = json.NewEncoder(os.Stdout).Encode(row{ID: test.ID, Type: test.Type, Expected: test.Expected, Actual: actual, Matched: ok, Action: result.Action})
	}
	fmt.Fprintf(os.Stderr, "matched %d/%d labeled edge cases\n", matched, len(cases))
}

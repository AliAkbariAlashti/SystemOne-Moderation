package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

type edgeCase struct { ID, Type, Text string }
type result struct { ID string `json:"id"`; Action string `json:"action"` }
type report struct {
	StartedAt string `json:"started_at"`
	Messages int `json:"messages"`
	Received int `json:"received"`
	DurationSeconds float64 `json:"duration_seconds"`
	ThroughputPerSecond float64 `json:"throughput_per_second"`
	LatencyMS map[string]float64 `json:"latency_ms"`
	Actions map[string]int `json:"actions"`
	Types map[string]int `json:"types"`
}

func main() {
	n := flag.Int("n", 100, "number of messages")
	casesPath := flag.String("cases", "testdata/edge_cases.json", "edge-case JSON")
	reportsDir := flag.String("reports", "reports", "output directory")
	flag.Parse()
	if *n < 1 { panic("n must be positive") }
	url := os.Getenv("AMQP_URL"); if url == "" { panic("AMQP_URL is required") }
	b, err := os.ReadFile(*casesPath); if err != nil { panic(err) }
	var cases []edgeCase; if err := json.Unmarshal(b, &cases); err != nil { panic(err) }
	conn, err := amqp091.Dial(url); if err != nil { panic(err) }; defer conn.Close()
	ch, err := conn.Channel(); if err != nil { panic(err) }; defer ch.Close()
	if _, err = ch.QueueDeclare("moderation.requests", true, false, false, false, nil); err != nil { panic(err) }
	replyQueue := fmt.Sprintf("loadtest.results.%d", time.Now().UnixNano())
	if _, err = ch.QueueDeclare(replyQueue, false, true, true, false, nil); err != nil { panic(err) }
	deliveries, err := ch.Consume(replyQueue, "", false, true, false, false, nil); if err != nil { panic(err) }

	sent := make(map[string]time.Time, *n)
	types, actions := map[string]int{}, map[string]int{}
	latencies := make([]float64, 0, *n)
	started := time.Now()
	for i := 0; i < *n; i++ {
		c := cases[i%len(cases)]
		id := fmt.Sprintf("load-%03d-%s", i+1, c.ID)
		body, _ := json.Marshal(map[string]string{"id": id, "text": c.Text, "type": c.Type})
		if err := ch.PublishWithContext(context.Background(), "", "moderation.requests", false, false, amqp091.Publishing{ContentType: "application/json", DeliveryMode: amqp091.Persistent, CorrelationId: id, ReplyTo: replyQueue, Body: body}); err != nil { panic(err) }
		sent[id] = time.Now(); types[c.Type]++
	}
	deadline := time.NewTimer(10 * time.Minute); defer deadline.Stop()
	for len(latencies) < *n {
		select {
		case d := <-deliveries:
			var r result; if err := json.Unmarshal(d.Body, &r); err != nil { _ = d.Nack(false, false); continue }
			if sentAt, ok := sent[r.ID]; ok { latencies = append(latencies, float64(time.Since(sentAt).Microseconds())/1000); actions[r.Action]++; _ = d.Ack(false) }
		case <-deadline.C: panic(fmt.Sprintf("timed out after receiving %d/%d results", len(latencies), *n))
		}
	}
	duration := time.Since(started).Seconds()
	sort.Float64s(latencies)
	r := report{StartedAt: started.UTC().Format(time.RFC3339), Messages: *n, Received: len(latencies), DurationSeconds: duration, ThroughputPerSecond: float64(len(latencies))/duration, LatencyMS: map[string]float64{"p50": percentile(latencies, .50), "p95": percentile(latencies, .95), "p99": percentile(latencies, .99), "min": latencies[0], "max": latencies[len(latencies)-1]}, Actions: actions, Types: types}
	if err := os.MkdirAll(*reportsDir, 0755); err != nil { panic(err) }
	stamp := started.UTC().Format("20060102T150405Z")
	jsonPath := filepath.Join(*reportsDir, "load-test-"+stamp+".json")
	file, err := os.Create(jsonPath); if err != nil { panic(err) }; _ = json.NewEncoder(file).Encode(r); _ = file.Close()
	writeLatencyChart(filepath.Join(*reportsDir, "load-test-latency-"+stamp+".svg"), r)
	writeActionChart(filepath.Join(*reportsDir, "load-test-actions-"+stamp+".svg"), r)
	encoded, _ := json.MarshalIndent(r, "", "  "); fmt.Println(string(encoded)); fmt.Fprintf(os.Stderr, "reports: %s\n", jsonPath)
}

func percentile(sorted []float64, p float64) float64 { i := int(math.Ceil(p*float64(len(sorted)))) - 1; if i < 0 { i = 0 }; return sorted[i] }
func writeLatencyChart(path string, r report) { labels := []string{"p50", "p95", "p99"}; max := r.LatencyMS["p99"]; var bars []string; for i, label := range labels { v := r.LatencyMS[label]; h := 240*v/max; x := 90+i*150; bars = append(bars, fmt.Sprintf(`<rect x="%d" y="%.0f" width="90" height="%.0f" fill="#2563eb"/><text x="%d" y="360" text-anchor="middle">%s</text><text x="%d" y="%.0f" text-anchor="middle">%.0f ms</text>`, x, 330-h, h, x+45, label, x+45, 320-h, v)) }; writeSVG(path, "End-to-end moderation latency", strings.Join(bars, "")) }
func writeActionChart(path string, r report) { labels := []string{"allow", "review", "block"}; max := 1; for _, n := range r.Actions { if n > max { max = n } }; colors := []string{"#16a34a", "#f59e0b", "#dc2626"}; var bars []string; for i, label := range labels { n := r.Actions[label]; h := 240*float64(n)/float64(max); x := 90+i*150; bars = append(bars, fmt.Sprintf(`<rect x="%d" y="%.0f" width="90" height="%.0f" fill="%s"/><text x="%d" y="360" text-anchor="middle">%s</text><text x="%d" y="%.0f" text-anchor="middle">%d</text>`, x, 330-h, h, colors[i], x+45, label, x+45, 320-h, n)) }; writeSVG(path, "Moderation actions", strings.Join(bars, "")) }
func writeSVG(path, title, bars string) { body := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="600" height="400" viewBox="0 0 600 400"><style>text{font-family:Arial,sans-serif;fill:#172033;font-size:16px}.title{font-size:22px;font-weight:bold}</style><rect width="600" height="400" fill="white"/><text class="title" x="40" y="45">%s</text><line x1="60" y1="330" x2="550" y2="330" stroke="#94a3b8"/>%s</svg>`, title, bars); _ = os.WriteFile(path, []byte(body), 0644) }

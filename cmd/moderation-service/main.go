package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/example/jev-moderation-service/internal/moderation"
	"github.com/rabbitmq/amqp091-go"
)

type app struct { apiKey, policyPath string }

func main() {
	apiKey := os.Getenv("TYPESAFE_API_KEY")
	if apiKey == "" { log.Fatal("TYPESAFE_API_KEY is required") }
	policyPath := env("POLICY_PATH", "config/policy.json")
	if _, err := moderation.LoadPolicy(policyPath); err != nil { log.Fatalf("invalid policy: %v", err) }
	a := app{apiKey: apiKey, policyPath: policyPath}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM); defer stop()
	if url := os.Getenv("AMQP_URL"); url != "" {
		for i := 1; i <= envInt("WORKER_CONCURRENCY", 1); i++ { go consumeAMQP(ctx, url, env("AMQP_INPUT_QUEUE", "moderation.requests"), env("AMQP_OUTPUT_QUEUE", "moderation.results"), fmt.Sprintf("moderation-worker-%d", i), a) }
	}
	if !envBool("HTTP_ENABLED", true) { <-ctx.Done(); return }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /v1/policy", a.policy)
	mux.HandleFunc("POST /v1/moderate", a.moderate)
	server := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: logging(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 40 * time.Second}
	go func() { <-ctx.Done(); shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel(); _ = server.Shutdown(shutdownCtx) }()
	log.Printf("moderation HTTP service listening on %s", server.Addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) { log.Fatal(err) }
}

func (a app) loadService() (*moderation.Service, error) {
	policy, err := moderation.LoadPolicy(a.policyPath); if err != nil { return nil, err }
	return moderation.NewService(moderation.NewClient(a.apiKey), policy), nil
}

func (a app) health(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) }
func (a app) policy(w http.ResponseWriter, _ *http.Request) {
	p, err := moderation.LoadPolicy(a.policyPath); if err != nil { writeError(w, http.StatusServiceUnavailable, err); return }
	writeJSON(w, http.StatusOK, p)
}
func (a app) moderate(w http.ResponseWriter, r *http.Request) {
	var input moderation.Request
	if err := decodeJSON(r, &input); err != nil { writeError(w, http.StatusBadRequest, err); return }
	service, err := a.loadService(); if err != nil { writeError(w, http.StatusServiceUnavailable, err); return }
	result, err := service.Moderate(r.Context(), input)
	if err != nil { writeError(w, http.StatusBadGateway, err); return }
	writeJSON(w, http.StatusOK, result)
}

func consumeAMQP(ctx context.Context, url, inputQueue, outputQueue, consumerTag string, a app) {
	for ctx.Err() == nil {
		conn, err := amqp091.Dial(url); if err != nil { log.Printf("RabbitMQ unavailable: %v; retrying", err); wait(ctx, 5*time.Second); continue }
		ch, err := conn.Channel(); if err != nil { _ = conn.Close(); wait(ctx, 5*time.Second); continue }
		if err = ch.Qos(1, 0, false); err == nil { _, err = ch.QueueDeclare(inputQueue, true, false, false, false, nil) }
		if err == nil { _, err = ch.QueueDeclare(outputQueue, true, false, false, false, nil) }
		if err != nil { log.Printf("RabbitMQ setup failed: %v", err); _ = ch.Close(); _ = conn.Close(); wait(ctx, 5*time.Second); continue }
		deliveries, err := ch.Consume(inputQueue, consumerTag, false, false, false, false, nil)
		if err != nil { _ = ch.Close(); _ = conn.Close(); wait(ctx, 5*time.Second); continue }
		log.Printf("RabbitMQ consumer %s connected: %s -> %s", consumerTag, inputQueue, outputQueue)
		for {
			select {
			case <-ctx.Done(): _ = ch.Close(); _ = conn.Close(); return
			case d, ok := <-deliveries:
				if !ok { _ = ch.Close(); _ = conn.Close(); goto reconnect }
				var input moderation.Request
				if err := json.Unmarshal(d.Body, &input); err != nil { log.Printf("invalid AMQP message: %v", err); _ = d.Reject(false); continue }
				service, err := a.loadService(); if err == nil { var result moderation.Result; result, err = service.Moderate(ctx, input); if err == nil { body, _ := json.Marshal(result); replyQueue := d.ReplyTo; if replyQueue == "" { replyQueue = outputQueue }; err = ch.PublishWithContext(ctx, "", replyQueue, false, false, amqp091.Publishing{ContentType: "application/json", DeliveryMode: amqp091.Persistent, Body: body, CorrelationId: d.CorrelationId}) } }
				if err != nil { log.Printf("AMQP moderation failed: %v", err); _ = d.Nack(false, true); continue }; _ = d.Ack(false)
			}
		}
		reconnect: wait(ctx, time.Second)
	}
}

func decodeJSON(r *http.Request, destination any) error { defer r.Body.Close(); decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20)); decoder.DisallowUnknownFields(); return decoder.Decode(destination) }
func writeJSON(w http.ResponseWriter, status int, value any) { w.Header().Set("Content-Type", "application/json"); w.WriteHeader(status); _ = json.NewEncoder(w).Encode(value) }
func writeError(w http.ResponseWriter, status int, err error) { writeJSON(w, status, map[string]string{"error": err.Error()}) }
func env(key, fallback string) string { if value := strings.TrimSpace(os.Getenv(key)); value != "" { return value }; return fallback }
func envBool(key string, fallback bool) bool { value := strings.TrimSpace(os.Getenv(key)); if value == "" { return fallback }; parsed, err := strconv.ParseBool(value); if err != nil { log.Printf("invalid %s=%q; using %t", key, value, fallback); return fallback }; return parsed }
func envInt(key string, fallback int) int { value := strings.TrimSpace(os.Getenv(key)); if value == "" { return fallback }; parsed, err := strconv.Atoi(value); if err != nil || parsed < 1 { log.Printf("invalid %s=%q; using %d", key, value, fallback); return fallback }; return parsed }
func wait(ctx context.Context, duration time.Duration) { select { case <-ctx.Done(): case <-time.After(duration): } }
func logging(next http.Handler) http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { start := time.Now(); next.ServeHTTP(w, r); log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond)) }) }

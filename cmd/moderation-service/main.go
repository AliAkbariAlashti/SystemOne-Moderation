package main

import (
	"context"
	"crypto/subtle"
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
	"sync"
	"syscall"
	"time"

	"github.com/example/jev-moderation-service/internal/moderation"
)

type app struct {
	apiKey, policyPath string
	token              string
}

func main() {
	apiKey := os.Getenv("TYPESAFE_API_KEY")
	if apiKey == "" {
		log.Fatal("TYPESAFE_API_KEY is required")
	}
	policyPath := env("POLICY_PATH", "config/policy.json")
	if _, err := moderation.LoadPolicy(policyPath); err != nil {
		log.Fatalf("invalid policy: %v", err)
	}
	a := app{apiKey: apiKey, policyPath: policyPath, token: os.Getenv("MODERATION_API_TOKEN")}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var workers sync.WaitGroup
	defer workers.Wait()
	if url := os.Getenv("AMQP_URL"); url != "" {
		for i := 1; i <= envInt("WORKER_CONCURRENCY", 1); i++ {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				consumeAMQP(ctx, url, env("AMQP_INPUT_QUEUE", "moderation.requests"), env("AMQP_OUTPUT_QUEUE", "moderation.results"), fmt.Sprintf("moderation-worker-%d", i), a)
			}(i)
		}
	}
	if !envBool("HTTP_ENABLED", true) {
		<-ctx.Done()
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /v1/policy", a.authorize(a.policy))
	mux.HandleFunc("POST /v1/moderate", a.authorize(a.moderate))
	server := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: logging(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 40 * time.Second}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
	}()
	log.Printf("moderation HTTP service listening on %s", server.Addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
}

func (a app) loadService() (*moderation.Service, error) {
	policy, err := moderation.LoadPolicy(a.policyPath)
	if err != nil {
		return nil, err
	}
	return moderation.NewService(moderation.NewClient(a.apiKey), policy), nil
}

func (a app) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (a app) policy(w http.ResponseWriter, _ *http.Request) {
	p, err := moderation.LoadPolicy(a.policyPath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
func (a app) moderate(w http.ResponseWriter, r *http.Request) {
	var input moderation.Request
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := decodeJSON(r, &input); err != nil {
		status := http.StatusBadRequest
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, err)
		return
	}
	if err := input.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	service, err := a.loadService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	result, err := service.Moderate(r.Context(), input)
	if err != nil {
		status := http.StatusBadGateway
		var invalid *moderation.ValidationError
		if errors.As(err, &invalid) {
			status = http.StatusBadRequest
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(r *http.Request, destination any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("invalid %s=%q; using %t", key, value, fallback)
		return fallback
	}
	return parsed
}
func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		log.Printf("invalid %s=%q; using %d", key, value, fallback)
		return fallback
	}
	return parsed
}
func wait(ctx context.Context, duration time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(duration):
	}
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func (a app) ready(w http.ResponseWriter, r *http.Request) {
	if _, err := moderation.LoadPolicy(a.policyPath); err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("policy unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
func (a app) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.token)) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		next(w, r)
	}
}

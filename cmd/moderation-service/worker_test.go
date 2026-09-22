package main

import (
	"context"
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
	"os"
	"testing"
	"time"
)

func TestBrokerDelivery(t *testing.T) {
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("set TEST_AMQP_URL for RabbitMQ integration")
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	prefix := fmt.Sprintf("moderation.test.%d", time.Now().UnixNano())
	for _, q := range []string{prefix, prefix + ".dead", prefix + ".results"} {
		if _, err := ch.QueueDeclare(q, true, false, false, false, nil); err != nil {
			t.Fatal(err)
		}
		defer ch.QueueDelete(q, false, false, false)
	}
	p, err := newPublisher(ch)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.publish(ctx, prefix+".missing", amqp.Publishing{Body: []byte("test")}); err == nil {
		t.Fatal("unroutable publish accepted")
	}
	if err := p.publish(ctx, prefix, amqp.Publishing{Body: []byte(`{"text":""}`), CorrelationId: "job-1", ReplyTo: prefix + ".results"}); err != nil {
		t.Fatal(err)
	}
	d, ok, err := ch.Get(prefix, false)
	if err != nil || !ok {
		t.Fatalf("get: %v, %v", ok, err)
	}
	if err := handleDelivery(ctx, p, d, prefix, prefix+".results", app{}); err != nil {
		t.Fatal(err)
	}
	dead, ok, err := ch.Get(prefix+".dead", true)
	if err != nil || !ok {
		t.Fatalf("dead letter missing: %v", err)
	}
	if dead.CorrelationId != "job-1" || dead.ReplyTo != prefix+".results" || string(dead.Body) != string(d.Body) || dead.Headers["moderation_failure"] != "processing_failed" {
		t.Fatal("lost recovery context")
	}
	if _, ok, err := ch.Get(prefix, true); err != nil || ok {
		t.Fatal("original job still queued")
	}
}

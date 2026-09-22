package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/example/jev-moderation-service/internal/moderation"
	amqp "github.com/rabbitmq/amqp091-go"
)

var errUnroutable = errors.New("reply queue is unavailable")

// One outstanding publish per channel keeps returns and confirmations correlated.
type publisher struct {
	ch       *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
}

func newPublisher(ch *amqp.Channel) (*publisher, error) {
	if err := ch.Confirm(false); err != nil {
		return nil, err
	}
	return &publisher{ch, ch.NotifyPublish(make(chan amqp.Confirmation, 1)), ch.NotifyReturn(make(chan amqp.Return, 1))}, nil
}
func (p *publisher) publish(ctx context.Context, queue string, msg amqp.Publishing) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.ch.PublishWithContext(ctx, "", queue, true, false, msg); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c, ok := <-p.confirms:
		if !ok || !c.Ack {
			return fmt.Errorf("publish not confirmed")
		}
		// RabbitMQ sends basic.return before basic.ack for an unroutable publish.
		select {
		case <-p.returns:
			return errUnroutable
		default:
			return nil
		}
	}
}
func consumeAMQP(ctx context.Context, url, input, output, tag string, a app) {
	for ctx.Err() == nil {
		if err := consumeSession(ctx, url, input, output, tag, a); err != nil && ctx.Err() == nil {
			log.Printf("worker %s disconnected; retrying", tag)
		}
		wait(ctx, 5*time.Second)
	}
}
func consumeSession(ctx context.Context, url, input, output, tag string, a app) error {
	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(10 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	for _, queue := range []string{input, output, input + ".dead"} {
		if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
			return err
		}
	}
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	pub, err := newPublisher(ch)
	if err != nil {
		return err
	}
	deliveries, err := ch.Consume(input, tag, false, false, false, false, nil)
	if err != nil {
		return err
	}
	log.Printf("worker %s ready", tag)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("consumer closed")
			}
			if err := handleDelivery(ctx, pub, d, input, output, a); err != nil {
				return err
			}
		}
	}
}
func handleDelivery(ctx context.Context, pub *publisher, d amqp.Delivery, input, output string, a app) error {
	var request moderation.Request
	var err error
	if len(d.Body) > 1<<20 {
		err = &moderation.ValidationError{Message: "request exceeds 1 MiB"}
	} else {
		r, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(d.Body))
		if decodeErr := decodeJSON(r, &request); decodeErr != nil {
			err = &moderation.ValidationError{Message: "invalid request JSON"}
		} else {
			err = request.Validate()
		}
	}
	var result moderation.Result
	if err == nil {
		// Hold the unacknowledged delivery during bounded exponential backoff. A crash
		// requeues it; provider retries are bounded within a worker session.
		for attempt := 0; attempt < 4; attempt++ {
			var service *moderation.Service
			service, err = a.loadService()
			if err == nil {
				result, err = service.Moderate(ctx, request)
			}
			if err == nil || !moderation.Retryable(err) || attempt == 3 {
				break
			}
			wait(ctx, time.Second<<attempt)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		log.Printf("moderation processing failed: %v", err)
		return park(ctx, pub, d, input+".dead", "processing_failed")
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	queue := d.ReplyTo
	if queue == "" {
		queue = output
	}
	msg := amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body, CorrelationId: d.CorrelationId, MessageId: d.MessageId, Timestamp: time.Now()}
	if err := pub.publish(ctx, queue, msg); err != nil {
		// A return is confirmed but unroutable; preserve the original job for recovery.
		// Other publish failures close the session and leave the delivery unacknowledged.
		if errors.Is(err, errUnroutable) {
			return park(ctx, pub, d, input+".dead", "reply_unroutable")
		}
		return err
	}
	return d.Ack(false)
}
func park(ctx context.Context, pub *publisher, d amqp.Delivery, queue, reason string) error {
	msg := amqp.Publishing{ContentType: d.ContentType, DeliveryMode: amqp.Persistent, Body: d.Body, CorrelationId: d.CorrelationId, MessageId: d.MessageId, ReplyTo: d.ReplyTo, Headers: amqp.Table{"moderation_failure": reason}, Timestamp: time.Now()}
	if err := pub.publish(ctx, queue, msg); err != nil {
		return err
	}
	log.Printf("moderation job moved to dead-letter queue: %s", reason)
	return d.Ack(false)
}

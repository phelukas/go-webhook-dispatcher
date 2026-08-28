package webhook

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrIdempotencyConflict = errors.New("idempotency key already used with different content")

// Submission is the validated input persisted before asynchronous delivery.
type Submission struct {
	IdempotencyKey string
	TargetURL      string
	EventType      string
	Payload        json.RawMessage
}

// Webhook represents a durable delivery request.
type Webhook struct {
	ID             string          `json:"id"`
	IdempotencyKey string          `json:"-"`
	TargetURL      string          `json:"target_url"`
	EventType      string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
}

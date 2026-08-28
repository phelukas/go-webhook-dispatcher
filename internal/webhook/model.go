package webhook

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrIdempotencyConflict = errors.New("idempotency key already used with different content")
var ErrLeaseLost = errors.New("webhook processing lease is no longer owned")

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
	AttemptCount   int             `json:"attempt_count"`
	CreatedAt      time.Time       `json:"created_at"`
}

// Claim is a webhook reserved for one worker under a fenced lease.
type Claim struct {
	Webhook
	LeaseToken string `json:"-"`
}

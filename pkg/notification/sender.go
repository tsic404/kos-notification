package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DeliveryTimeout bounds one attempt's network call, so an unresponsive channel
// cannot hold a delivery round open forever (KOSD-C-06, NOTIFY-C-4).
const DeliveryTimeout = 10 * time.Second

// Message is what one attempt posts to a channel. The record carries the
// durable facts of the attempt; the payload carries what the channel needs to
// react now.
type Message struct {
	// Subscription is the rule that matched.
	Subscription string `json:"subscription"`
	// Channel is the target channel's name.
	Channel string `json:"channel"`
	// Source is the object the event or the state change is about.
	Source ObjectRef `json:"source"`
	// Kind is Event or State.
	Kind TriggerKind `json:"kind"`
	// Reason is the event reason; a state change has none.
	Reason string `json:"reason,omitempty"`
	// At is the moment the trigger happened.
	At time.Time `json:"at"`
}

// Sender posts one message to one channel. The secret is handed to the sender
// for one attempt only and never enters a record or a log (NOTIFY-C-5,
// KOSD-C-14).
type Sender interface {
	Send(ctx context.Context, channel Channel, secret string, message Message) error
}

// Webhook posts the message as JSON and accepts a 2xx answer. Any other answer
// is a failure whose reason is readable on the attempt's record, so a refused
// delivery is not dropped silently (NOTIFY-C-4).
type Webhook struct {
	// Client sends the request; defaults to a client bounded by DeliveryTimeout.
	Client *http.Client
}

// Send posts one message and judges the answer.
func (w Webhook) Send(ctx context.Context, channel Channel, secret string, message Message) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode the message for channel %s: %w", channel.Name, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, channel.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build the request for channel %s: %w", channel.Name, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := w.client().Do(request)
	if err != nil {
		return fmt.Errorf("post to channel %s: %w", channel.Name, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("channel %s answered %s", channel.Name, response.Status)
	}
	return nil
}

// client returns the request client, bounded by default.
func (w Webhook) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	return &http.Client{Timeout: DeliveryTimeout}
}

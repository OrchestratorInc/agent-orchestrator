// Package push delivers dashboard notifications to registered phones as OS push
// notifications via the Expo Push Service (exp.host), which relays to APNs/FCM.
// The daemon is the sender: a dispatcher subscribes to the in-process notification
// hub and POSTs each new notification to every registered device.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Expo Push Service endpoints.
const (
	expoSendURL     = "https://exp.host/--/api/v2/push/send"
	expoReceiptsURL = "https://exp.host/--/api/v2/push/getReceipts"
)

// Message is one Expo push message targeting a single device token.
type Message struct {
	To        string         `json:"to"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Data      map[string]any `json:"data,omitempty"`
	Sound     string         `json:"sound,omitempty"`
	Priority  string         `json:"priority,omitempty"`
	ChannelID string         `json:"channelId,omitempty"`
	// CategoryID picks the notification's action buttons on the phone.
	CategoryID string `json:"categoryId,omitempty"`
}

// Ticket is Expo's synchronous per-message result. Status is "ok" or "error";
// on error, Details.Error carries a code such as "DeviceNotRegistered". A ticket
// with Status "ok" carries an ID used to fetch the later delivery Receipt.
type Ticket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

// IsDeviceNotRegistered reports whether the ticket says the target token is dead
// (uninstalled or rotated) and should be pruned from the registry.
func (t Ticket) IsDeviceNotRegistered() bool {
	return t.Status == "error" && t.Details.Error == "DeviceNotRegistered"
}

// Receipt is Expo's asynchronous per-message delivery result, fetched by ticket
// ID ~15 minutes after send. It is the only place some failures surface — most
// importantly a token that died after Expo accepted the message.
type Receipt struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

// IsDeviceNotRegistered reports whether the receipt says the token is dead and
// should be pruned from the registry.
func (r Receipt) IsDeviceNotRegistered() bool {
	return r.Status == "error" && r.Details.Error == "DeviceNotRegistered"
}

// ExpoClient sends messages to the Expo Push Service. The zero value is not
// usable; construct it with NewExpoClient.
type ExpoClient struct {
	http        *http.Client
	sendURL     string
	receiptsURL string
	accessToken string // optional; enables Expo enforced push security when set
}

// NewExpoClient constructs a client. accessToken is optional (empty sends
// unauthenticated, which the Expo Push Service accepts by default).
func NewExpoClient(accessToken string) *ExpoClient {
	return &ExpoClient{
		http:        &http.Client{Timeout: 15 * time.Second},
		sendURL:     expoSendURL,
		receiptsURL: expoReceiptsURL,
		accessToken: accessToken,
	}
}

const (
	// maxBatch is the Expo Push Service per-request message cap.
	maxBatch = 100
	// maxReceiptBatch is the getReceipts per-request id cap.
	maxReceiptBatch = 300
	// maxSendAttempts bounds the transient-error retry (D3): a momentary
	// network/5xx blip is retried a couple times, but we never queue.
	maxSendAttempts = 3
)

// retryBaseDelay is the first backoff step (doubled each attempt). A var so tests
// can shrink it.
var retryBaseDelay = 500 * time.Millisecond

// retryableError marks a send failure worth retrying (transport error, 5xx, 429).
// A 4xx or a decode error is not wrapped, so it fails fast.
type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

func isRetryable(err error) bool {
	var r retryableError
	return errors.As(err, &r)
}

// Send delivers messages to Expo and returns one ticket per message, in the same
// order. It chunks into batches of 100 (Expo's cap) and retries a batch a few
// times on a transient error (D3: bounded retry, no durable outbox). A batch that
// keeps failing aborts and is returned; tickets from earlier batches are still
// returned.
func (c *ExpoClient) Send(ctx context.Context, messages []Message) ([]Ticket, error) {
	tickets := make([]Ticket, 0, len(messages))
	for start := 0; start < len(messages); start += maxBatch {
		end := start + maxBatch
		if end > len(messages) {
			end = len(messages)
		}
		batch, err := c.sendBatchWithRetry(ctx, messages[start:end])
		if err != nil {
			return tickets, err
		}
		tickets = append(tickets, batch...)
	}
	return tickets, nil
}

func (c *ExpoClient) sendBatchWithRetry(ctx context.Context, batch []Message) ([]Ticket, error) {
	var lastErr error
	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		tickets, err := c.sendBatch(ctx, batch)
		if err == nil {
			return tickets, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxSendAttempts {
			return nil, err
		}
		// Exponential backoff, but abort promptly if the daemon is shutting down.
		delay := retryBaseDelay * time.Duration(1<<(attempt-1))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

func (c *ExpoClient) sendBatch(ctx context.Context, batch []Message) ([]Ticket, error) {
	body, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	res, err := c.post(ctx, c.sendURL, body)
	if err != nil {
		return nil, retryableError{err} // transport error — retry
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		return nil, retryableError{fmt.Errorf("expo push: status %d", res.StatusCode)}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("expo push: unexpected status %d", res.StatusCode)
	}
	var envelope struct {
		Data []Ticket `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("expo push: decode response: %w", err)
	}
	return envelope.Data, nil
}

// GetReceipts fetches delivery receipts for the given ticket ids (chunked to the
// getReceipts cap). Returns a map keyed by ticket id; ids with no receipt yet are
// simply absent.
func (c *ExpoClient) GetReceipts(ctx context.Context, ids []string) (map[string]Receipt, error) {
	out := make(map[string]Receipt, len(ids))
	for start := 0; start < len(ids); start += maxReceiptBatch {
		end := start + maxReceiptBatch
		if end > len(ids) {
			end = len(ids)
		}
		body, err := json.Marshal(map[string][]string{"ids": ids[start:end]})
		if err != nil {
			return out, err
		}
		res, err := c.post(ctx, c.receiptsURL, body)
		if err != nil {
			return out, err
		}
		var envelope struct {
			Data map[string]Receipt `json:"data"`
		}
		decodeErr := json.NewDecoder(res.Body).Decode(&envelope)
		_ = res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return out, fmt.Errorf("expo receipts: unexpected status %d", res.StatusCode)
		}
		if decodeErr != nil {
			return out, fmt.Errorf("expo receipts: decode response: %w", decodeErr)
		}
		for id, r := range envelope.Data {
			out[id] = r
		}
	}
	return out, nil
}

func (c *ExpoClient) post(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	return c.http.Do(req)
}

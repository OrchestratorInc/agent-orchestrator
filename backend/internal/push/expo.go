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

const (
	expoSendURL     = "https://exp.host/--/api/v2/push/send"
	expoReceiptsURL = "https://exp.host/--/api/v2/push/getReceipts"
)

type Message struct {
	To        string         `json:"to"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Data      map[string]any `json:"data,omitempty"`
	Sound     string         `json:"sound,omitempty"`
	Priority  string         `json:"priority,omitempty"`
	ChannelID string         `json:"channelId,omitempty"`
}

type Ticket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

func (t Ticket) IsDeviceNotRegistered() bool {
	return t.Status == "error" && t.Details.Error == "DeviceNotRegistered"
}

type Receipt struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

func (r Receipt) IsDeviceNotRegistered() bool {
	return r.Status == "error" && r.Details.Error == "DeviceNotRegistered"
}

type ExpoClient struct {
	http        *http.Client
	sendURL     string
	receiptsURL string
	accessToken string
}

func NewExpoClient(accessToken string) *ExpoClient {
	return &ExpoClient{
		http:        &http.Client{Timeout: 15 * time.Second},
		sendURL:     expoSendURL,
		receiptsURL: expoReceiptsURL,
		accessToken: accessToken,
	}
}

const (
	maxBatch        = 100
	maxReceiptBatch = 300
	maxSendAttempts = 3
)

var retryBaseDelay = 500 * time.Millisecond

type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

func isRetryable(err error) bool {
	var r retryableError
	return errors.As(err, &r)
}

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
		return nil, retryableError{err}
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

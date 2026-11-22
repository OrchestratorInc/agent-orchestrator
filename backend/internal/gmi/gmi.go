// Package gmi is a client for GMI Cloud's generation API.
//
// GMI Cloud publishes one API family for media generation: a catalog of
// models under /models, and a request queue under /requests. A request is
// submitted with a model and a payload, and its media arrives asynchronously
// once the request reaches the success state.
//
// Every request carries the account's API key as a bearer token and an
// explicit User-Agent. GMI's edge rejects the default Go and Python user
// agents with a 403 before the request reaches the API, so a client that
// forgets the header fails with "access denied" instead of an API error.
//
// The key never appears in a URL, a log line, or an error message.
package gmi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the root of GMI Cloud's media generation API.
	DefaultBaseURL = "https://console.gmicloud.ai/api/v1/ie/requestqueue/apikey"

	// UserAgent identifies AO to GMI Cloud. The edge rejects the default
	// Go-http-client agent with a 403, so every request sets a real one.
	UserAgent = "agent-orchestrator/1.0 (+GMI Cloud client)"

	// maxBodyBytes bounds one response body. The catalog is the largest
	// answer here and it stays well inside this.
	maxBodyBytes = 16 << 20
)

// ErrNoKey reports that no API key was supplied.
var ErrNoKey = errors.New("gmi: an API key is required")

// APIError is a rejection from GMI Cloud. It carries the provider's message
// but never the key that was sent.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("gmi: GMI Cloud returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("gmi: GMI Cloud returned HTTP %d: %s", e.StatusCode, e.Message)
}

// Client talks to GMI Cloud's generation API. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New returns a client for GMI Cloud's production generation API.
func New(apiKey string) (*Client, error) {
	return NewWithBaseURL(DefaultBaseURL, apiKey)
}

// NewWithBaseURL returns a client for a specific deployment of the
// generation API. It exists so tests can point at a local server.
func NewWithBaseURL(baseURL, apiKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("gmi: a base URL is required")
	}
	if !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
		return nil, errors.New("gmi: the base URL must be http or https")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrNoKey
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// BaseURL returns the API root this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// ListModels returns the models the account can see.
//
// The catalog answers with a bare list of model ids, and with full entries
// on some deployments; both are read here, so a caller sees one shape.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var payload struct {
		Data     []Model  `json:"data"`
		Models   []Model  `json:"models"`
		Results  []Model  `json:"results"`
		ModelIDs []string `json:"model_ids"`
	}
	if err := c.do(ctx, http.MethodGet, "/models", nil, &payload); err != nil {
		return nil, err
	}
	models := payload.Data
	if len(models) == 0 {
		models = payload.Models
	}
	if len(models) == 0 {
		models = payload.Results
	}
	for _, id := range payload.ModelIDs {
		models = append(models, Model{ModelID: id, DisplayName: id})
	}
	return models, nil
}

// GetModel returns one model's published description: its parameters, its
// price, and whether it is available.
func (c *Client) GetModel(ctx context.Context, modelID string) (*Model, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, errors.New("gmi: a model id is required")
	}
	var model Model
	if err := c.do(ctx, http.MethodGet, "/models/"+pathEscape(modelID), nil, &model); err != nil {
		return nil, err
	}
	if model.ModelID == "" {
		model.ModelID = modelID
	}
	return &model, nil
}

// Submit queues a generation and returns the request id that tracks it.
func (c *Client) Submit(ctx context.Context, modelID string, payload map[string]any) (string, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", errors.New("gmi: a model id is required")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	body := map[string]any{"model": modelID, "payload": payload}
	var response struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/requests", body, &response); err != nil {
		return "", err
	}
	requestID := strings.TrimSpace(response.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(response.ID)
	}
	if requestID == "" {
		return "", errors.New("gmi: GMI Cloud accepted the generation but returned no request id")
	}
	return requestID, nil
}

// GetRequest reads one generation request's current state. A request that
// has finished carries its media under Outcome.
func (c *Client) GetRequest(ctx context.Context, requestID string) (*Request, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, errors.New("gmi: a request id is required")
	}
	var request Request
	if err := c.do(ctx, http.MethodGet, "/requests/"+pathEscape(requestID), nil, &request); err != nil {
		return nil, err
	}
	if request.RequestID == "" {
		request.RequestID = requestID
	}
	return &request, nil
}

// ListRequests returns the account's most recent generation requests, newest
// first. A limit below one returns every request the API lists.
func (c *Client) ListRequests(ctx context.Context, limit int) ([]Request, error) {
	path := "/requests"
	if limit > 0 {
		path += "?limit=" + itoa(limit)
	}
	var payload struct {
		Data     []Request `json:"data"`
		Requests []Request `json:"requests"`
		Results  []Request `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	requests := payload.Data
	if len(requests) == 0 {
		requests = payload.Requests
	}
	if len(requests) == 0 {
		requests = payload.Results
	}
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
	}
	return requests, nil
}

// UploadURL asks GMI Cloud for a place to put one input file.
//
// The answer carries a temporary pre-signed destination and the public URL a
// generation request refers to. The upload URL expires in about fifteen
// minutes, so the file is put there straight away.
func (c *Client) UploadURL(ctx context.Context, fileType string) (uploadURL, publicURL string, err error) {
	fileType = strings.ToLower(strings.TrimSpace(fileType))
	if fileType == "" {
		return "", "", errors.New("gmi: a file type is required")
	}
	var response struct {
		UploadURL string `json:"upload_url"`
		PublicURL string `json:"public_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/upload-url", map[string]any{"file_type": fileType}, &response); err != nil {
		return "", "", err
	}
	uploadURL = strings.TrimSpace(response.UploadURL)
	publicURL = strings.TrimSpace(response.PublicURL)
	if uploadURL == "" {
		return "", "", errors.New("gmi: GMI Cloud returned no upload url")
	}
	return uploadURL, publicURL, nil
}

// Upload puts the bytes of one file at a pre-signed upload URL.
//
// The upload URL is signed by GMI Cloud and carries its own authorization, so
// the API key is not sent with the bytes: the key belongs to the API, not to
// the file store.
func Upload(ctx context.Context, uploadURL, contentType string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gmi: build upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := uploadClient.Do(req)
	if err != nil {
		return fmt.Errorf("gmi: upload file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{StatusCode: resp.StatusCode, Message: providerMessage(body)}
	}
	return nil
}

// uploadClient puts file bytes at a pre-signed URL. It is separate from the
// API client so the API key never travels with the file.
var uploadClient = &http.Client{Timeout: 5 * time.Minute}

// do performs one request and decodes a 2xx body into out. body nil sends no
// payload.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gmi: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("gmi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gmi: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("gmi: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{StatusCode: resp.StatusCode, Message: providerMessage(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("gmi: decode response: %w", err)
	}
	return nil
}

// providerMessage pulls the provider's own message out of an error body.
// GMI answers with {"error": "..."} or {"error": {"message": "..."}}; a body
// of any other shape is reported as it arrived, bounded.
func providerMessage(body []byte) string {
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		if text := strings.TrimSpace(envelope.Message); text != "" {
			return text
		}
		raw := strings.TrimSpace(string(envelope.Error))
		if raw == "" || raw == "null" {
			return ""
		}
		var text string
		if err := json.Unmarshal(envelope.Error, &text); err == nil {
			return strings.TrimSpace(text)
		}
		var object struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(envelope.Error, &object); err == nil {
			return strings.TrimSpace(object.Message)
		}
	}
	return truncate(strings.TrimSpace(string(body)), 300)
}

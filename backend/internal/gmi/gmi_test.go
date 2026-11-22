package gmi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewWithBaseURL(server.URL, "test-key")
	if err != nil {
		t.Fatalf("NewWithBaseURL: %v", err)
	}
	return client
}

func TestNewRejectsAnEmptyKey(t *testing.T) {
	if _, err := New("   "); !errors.Is(err, ErrNoKey) {
		t.Fatalf("New(\"  \") error = %v, want ErrNoKey", err)
	}
}

func TestListModelsReadsEveryPublishedShape(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{name: "data", body: `{"data":[{"model_id":"a"},{"model":"b"}]}`, want: []string{"a", "b"}},
		{name: "models", body: `{"models":[{"model_id":"a"}]}`, want: []string{"a"}},
		{name: "results", body: `{"results":[{"model":"a"}]}`, want: []string{"a"}},
		{name: "model_ids", body: `{"model_ids":["a","b"]}`, want: []string{"a", "b"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testCase.body))
			})
			models, err := client.ListModels(context.Background())
			if err != nil {
				t.Fatalf("ListModels: %v", err)
			}
			got := make([]string, 0, len(models))
			for _, model := range models {
				got = append(got, model.ModelID)
			}
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("ListModels ids = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestRequestsCarryTheBearerTokenAndAnExplicitUserAgent(t *testing.T) {
	var seen *http.Request
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if _, err := client.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if seen == nil {
		t.Fatal("the server saw no request")
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want the bearer token", got)
	}
	// GMI's edge rejects the default Go agent with a 403, so the client has to
	// name itself.
	if got := seen.Header.Get("User-Agent"); got != UserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, UserAgent)
	}
}

func TestAPIErrorCarriesTheProviderMessageAndNotTheKey(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	})
	_, err := client.ListModels(context.Background())
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("ListModels error = %v, want an *APIError", err)
	}
	if apiError.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want 401", apiError.StatusCode)
	}
	if !strings.Contains(apiError.Error(), "invalid api key") {
		t.Fatalf("error = %q, want the provider's message", apiError.Error())
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("error = %q, must not carry the key", err.Error())
	}
}

func TestSubmitSendsTheModelAndItsPayload(t *testing.T) {
	var body map[string]any
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req-1"}`))
	})
	requestID, err := client.Submit(context.Background(), "seedream-4", map[string]any{"prompt": "a cat"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if requestID != "req-1" {
		t.Fatalf("request id = %q, want req-1", requestID)
	}
	if body["model"] != "seedream-4" {
		t.Fatalf("model = %v, want seedream-4", body["model"])
	}
	payload, ok := body["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want an object", body["payload"])
	}
	if payload["prompt"] != "a cat" {
		t.Fatalf("payload prompt = %v, want the prompt", payload["prompt"])
	}
}

func TestGetRequestReadsTheMediaOfAFinishedRequest(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req-1","status":"success","outcome":{"media_urls":["https://cdn/a.png"],"video_url":"https://cdn/b.mp4"}}`))
	})
	request, err := client.GetRequest(context.Background(), "req-1")
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if !request.Succeeded() {
		t.Fatalf("status = %q, want success", request.Status)
	}
	urls := request.MediaURLs()
	if len(urls) != 2 || urls[0] != "https://cdn/a.png" || urls[1] != "https://cdn/b.mp4" {
		t.Fatalf("media urls = %v, want both urls", urls)
	}
}

func TestUploadPutsTheBytesWithoutTheAPIKey(t *testing.T) {
	var seen *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := Upload(context.Background(), server.URL+"/upload", "image/png", []byte("bytes")); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if seen == nil {
		t.Fatal("the server saw no request")
	}
	if got := seen.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want none on a pre-signed upload", got)
	}
	if string(body) != "bytes" {
		t.Fatalf("uploaded body = %q, want the file bytes", body)
	}
}

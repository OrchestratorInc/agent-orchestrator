package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoGETRawBoundedLargeTrace(t *testing.T) {
	const totalSize = 10 * 1024 * 1024
	const tailMarker = "TAIL-MARKER-LINE\n"

	tailBytes := []byte(strings.Repeat("x", jobLogTailMaxBytes-len(tailMarker)) + tailMarker)
	body := make([]byte, totalSize)
	for i := 0; i < totalSize-len(tailBytes); i++ {
		body[i] = 'A'
	}
	copy(body[totalSize-len(tailBytes):], tailBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: srv.URL + "/api/v4",
	})

	got, err := c.doGETRaw(context.Background(), "/projects/1/jobs/1/trace")
	if err != nil {
		t.Fatalf("doGETRaw: unexpected error: %v", err)
	}
	if len(got) > jobLogTailMaxBytes {
		t.Errorf("doGETRaw retained %d bytes; want <= %d (jobLogTailMaxBytes) — body must be bounded", len(got), jobLogTailMaxBytes)
	}
	if !strings.HasSuffix(string(got), tailMarker) {
		t.Errorf("doGETRaw did not retain the rolling tail: last bytes = %q...; want suffix %q", trim(string(got), 60), tailMarker)
	}
	if len(got) == totalSize {
		t.Errorf("doGETRaw retained the entire 10 MB body — bound is not applied")
	}
}

func TestDoGETRawBoundedNormalTrace(t *testing.T) {
	lines := make([]string, 20)
	for i := 0; i < 20; i++ {
		lines[i] = strings.Repeat("y", 480) + " END"
	}
	body := strings.Join(lines, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: srv.URL + "/api/v4",
	})

	got, err := c.doGETRaw(context.Background(), "/projects/1/jobs/1/trace")
	if err != nil {
		t.Fatalf("doGETRaw: unexpected error: %v", err)
	}
	if string(got) != body {
		t.Errorf("doGETRaw returned %d bytes; want full %d-byte trace (no degradation in the normal case)", len(got), len(body))
	}
	tail := tailLines(string(got), ciFailureLogTailLines)
	gotLines := strings.Split(tail, "\n")
	if len(gotLines) != 20 {
		t.Errorf("tailLines(bounded) returned %d lines; want 20 (no degradation for a 10 KB trace)", len(gotLines))
	}
}

func TestDoGETRawErrorBodyCapped(t *testing.T) {
	const errSize = 1 * 1024 * 1024
	errBody := []byte(strings.Repeat("E", errSize))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(errBody)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: srv.URL + "/api/v4",
	})

	_, err := c.doGETRaw(context.Background(), "/projects/1/jobs/1/trace")
	if err == nil {
		t.Fatal("doGETRaw: error = nil, want an error for HTTP 500")
	}
	_ = errors.Unwrap(err)
}

func TestDoGETRawErrorBodyCappedExact(t *testing.T) {
	const errSize = 8 * 1024
	errBody := strings.Repeat("Z", errSize)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(errBody))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: srv.URL + "/api/v4",
	})

	_, err := c.doGETRaw(context.Background(), "/projects/1/jobs/1/trace")
	if err == nil {
		t.Fatal("doGETRaw: error = nil, want an error for HTTP 502")
	}
	if msg := err.Error(); len(msg) >= errSize {
		t.Errorf("error message length = %d; want < %d — error body must be capped at errorBodyMaxBytes before classification", len(msg), errSize)
	}
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

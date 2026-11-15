package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestExpoClientSendBuildsRequestAndParsesTickets(t *testing.T) {
	var gotAuth string
	var gotBody []Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"t1"},{"status":"error","message":"gone","details":{"error":"DeviceNotRegistered"}}]}`))
	}))
	defer srv.Close()

	c := NewExpoClient("secret-token")
	c.sendURL = srv.URL

	tickets, err := c.Send(context.Background(), []Message{
		{To: "ExponentPushToken[a]", Title: "hi", Body: "there"},
		{To: "ExponentPushToken[b]", Title: "yo", Body: "gone"},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("auth header = %q, want Bearer secret-token", gotAuth)
	}
	if len(gotBody) != 2 || gotBody[0].To != "ExponentPushToken[a]" || gotBody[0].Title != "hi" {
		t.Fatalf("request body = %+v", gotBody)
	}
	if len(tickets) != 2 {
		t.Fatalf("tickets = %d, want 2", len(tickets))
	}
	if tickets[0].Status != "ok" || tickets[0].ID != "t1" {
		t.Fatalf("ticket[0] = %+v", tickets[0])
	}
	if !tickets[1].IsDeviceNotRegistered() {
		t.Fatalf("ticket[1] should be DeviceNotRegistered: %+v", tickets[1])
	}
}

func TestExpoClientNoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"t1"}]}`))
	}))
	defer srv.Close()

	c := NewExpoClient("")
	c.sendURL = srv.URL
	if _, err := c.Send(context.Background(), []Message{{To: "ExponentPushToken[a]"}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if hadAuth {
		t.Fatal("expected no Authorization header when access token is empty")
	}
}

func TestExpoClientChunksBatches(t *testing.T) {
	var batchSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []Message
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &batch)
		batchSizes = append(batchSizes, len(batch))
		out := struct {
			Data []Ticket `json:"data"`
		}{Data: make([]Ticket, len(batch))}
		for i := range out.Data {
			out.Data[i] = Ticket{Status: "ok"}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	c := NewExpoClient("")
	c.sendURL = srv.URL
	msgs := make([]Message, 250)
	for i := range msgs {
		msgs[i] = Message{To: "ExponentPushToken[x]"}
	}
	tickets, err := c.Send(context.Background(), msgs)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(tickets) != 250 {
		t.Fatalf("tickets = %d, want 250", len(tickets))
	}
	if len(batchSizes) != 3 || batchSizes[0] != 100 || batchSizes[1] != 100 || batchSizes[2] != 50 {
		t.Fatalf("batch sizes = %v, want [100 100 50]", batchSizes)
	}
}

func TestExpoClientErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewExpoClient("")
	c.sendURL = srv.URL
	if _, err := c.Send(context.Background(), []Message{{To: "ExponentPushToken[a]"}}); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestExpoClientRetriesTransientThenSucceeds(t *testing.T) {
	orig := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = orig }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"t1"}]}`))
	}))
	defer srv.Close()

	c := NewExpoClient("")
	c.sendURL = srv.URL
	tickets, err := c.Send(context.Background(), []Message{{To: "ExponentPushToken[a]"}})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(tickets) != 1 || tickets[0].ID != "t1" {
		t.Fatalf("tickets = %+v", tickets)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3 (2 retries then success)", got)
	}
}

func TestExpoClientDoesNotRetry4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewExpoClient("")
	c.sendURL = srv.URL
	if _, err := c.Send(context.Background(), []Message{{To: "x"}}); err == nil {
		t.Fatal("expected error on 400")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestExpoClientGetReceipts(t *testing.T) {
	var gotBody map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"data":{"t1":{"status":"ok"},"t2":{"status":"error","details":{"error":"DeviceNotRegistered"}}}}`))
	}))
	defer srv.Close()

	c := NewExpoClient("")
	c.receiptsURL = srv.URL
	receipts, err := c.GetReceipts(context.Background(), []string{"t1", "t2"})
	if err != nil {
		t.Fatalf("getReceipts: %v", err)
	}
	if len(gotBody["ids"]) != 2 {
		t.Fatalf("request ids = %v", gotBody["ids"])
	}
	if receipts["t1"].Status != "ok" || !receipts["t2"].IsDeviceNotRegistered() {
		t.Fatalf("receipts = %+v", receipts)
	}
}

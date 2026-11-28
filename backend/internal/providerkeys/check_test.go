package providerkeys

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withCheckEndpoint(t *testing.T, name string, endpoint checkEndpoint) {
	t.Helper()
	previous, had := checkEndpoints[name]
	checkEndpoints[name] = endpoint
	t.Cleanup(func() {
		if had {
			checkEndpoints[name] = previous
		} else {
			delete(checkEndpoints, name)
		}
	})
}

func TestCheckListsModelsForAcceptedKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"},{"id":"a-model"},{"id":"bad\nid"}]}`))
	}))
	t.Cleanup(server.Close)
	withCheckEndpoint(t, "OPENAI_API_KEY", checkEndpoint{url: server.URL, authorize: bearer})
	result, err := Check(context.Background(), "OPENAI_API_KEY", "good")
	if err != nil || result.Status != CheckValid || len(result.Models) != 2 || result.Models[0] != "a-model" {
		t.Fatalf("Check(good) = %+v, %v", result, err)
	}
	result, err = Check(context.Background(), "OPENAI_API_KEY", "bad")
	if err != nil || result.Status != CheckInvalid || len(result.Models) != 0 {
		t.Fatalf("Check(bad) = %+v, %v", result, err)
	}
}

func TestCheckStripsGeminiPrefixAndReportsOutages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "down" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"}]}`))
	}))
	t.Cleanup(server.Close)
	withCheckEndpoint(t, "GEMINI_API_KEY", checkEndpoint{url: server.URL, prefix: "models/", authorize: func(req *http.Request, key string) {
		req.Header.Set("x-goog-api-key", key)
	}})
	result, err := Check(context.Background(), "GEMINI_API_KEY", "key")
	if err != nil || result.Status != CheckValid || len(result.Models) != 1 || result.Models[0] != "gemini-2.5-pro" {
		t.Fatalf("Check = %+v, %v", result, err)
	}
	result, err = Check(context.Background(), "GEMINI_API_KEY", "down")
	if err != nil || result.Status != CheckUnreachable {
		t.Fatalf("Check(down) = %+v, %v", result, err)
	}
}

func TestCheckValidatesPublicListsSeparately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key" && r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-5"}]}`))
	}))
	t.Cleanup(server.Close)
	withCheckEndpoint(t, "OPENROUTER_API_KEY", checkEndpoint{url: server.URL + "/models", validateURL: server.URL + "/key", authorize: bearer})
	if result, _ := Check(context.Background(), "OPENROUTER_API_KEY", "bad"); result.Status != CheckInvalid {
		t.Fatalf("public list accepted a bad key: %+v", result)
	}
	if result, _ := Check(context.Background(), "OPENROUTER_API_KEY", "good"); result.Status != CheckValid || len(result.Models) != 1 {
		t.Fatalf("Check(good) = %+v", result)
	}
}

func TestCheckLeavesUnknownProvidersUnverified(t *testing.T) {
	result, err := Check(context.Background(), "CURSOR_API_KEY", "key")
	if err != nil || result.Status != CheckUnverified || Checkable("CURSOR_API_KEY") {
		t.Fatalf("Check = %+v, %v", result, err)
	}
	if _, err := Check(context.Background(), "OPENAI_API_KEY", "line\nbreak"); err == nil {
		t.Fatal("accepted a multiline key")
	}
}

func TestCheckReadsEachProvidersRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`))
	}))
	t.Cleanup(server.Close)
	// Gemini answers a mistyped key with 400, which must not read as an outage.
	withCheckEndpoint(t, "GEMINI_API_KEY", checkEndpoint{url: server.URL, authorize: bearer, rejectsWith400: true})
	if result, err := Check(context.Background(), "GEMINI_API_KEY", "typo"); err != nil || result.Status != CheckInvalid {
		t.Fatalf("Check(Gemini 400) = %+v, %v", result, err)
	}
	withCheckEndpoint(t, "OPENAI_API_KEY", checkEndpoint{url: server.URL, authorize: bearer})
	if result, err := Check(context.Background(), "OPENAI_API_KEY", "key"); err != nil || result.Status != CheckUnreachable {
		t.Fatalf("Check(OpenAI 400) = %+v, %v", result, err)
	}
	// A key for another region's endpoint is not proven wrong.
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	t.Cleanup(rejecting.Close)
	withCheckEndpoint(t, "MOONSHOT_API_KEY", checkEndpoint{url: rejecting.URL, authorize: bearer, regional: true})
	if result, err := Check(context.Background(), "MOONSHOT_API_KEY", "cn-key"); err != nil || result.Status != CheckUnverified {
		t.Fatalf("Check(regional 401) = %+v, %v", result, err)
	}
}

func TestCheckNeverFollowsARedirectWithTheKey(t *testing.T) {
	leaked := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked <- r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(target.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	withCheckEndpoint(t, "ANTHROPIC_API_KEY", checkEndpoint{url: redirecting.URL, authorize: func(req *http.Request, key string) { req.Header.Set("x-api-key", key) }})
	if result, err := Check(context.Background(), "ANTHROPIC_API_KEY", "secret"); err != nil || result.Status != CheckUnreachable {
		t.Fatalf("Check(redirect) = %+v, %v", result, err)
	}
	select {
	case key := <-leaked:
		t.Fatalf("followed the redirect and sent %q", key)
	default:
	}
}

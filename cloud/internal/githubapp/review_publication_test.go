package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewMarkerReconciliationScansPastFirstPage(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/octo/widgets/pulls/17/reviews" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		pages++
		if r.URL.Query().Get("page") == "1" {
			reviews := make([]map[string]any, 100)
			for i := range reviews {
				reviews[i] = map[string]any{"id": i + 1, "body": "unrelated review"}
			}
			_ = json.NewEncoder(w).Encode(reviews)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 101, "body": "Looks good\n\n<!-- ao-review-run:run-17 -->"}})
	}))
	defer server.Close()
	client := NewRESTClient(server.URL, server.Client())
	id, err := client.FindPullRequestReviewByMarker(context.Background(), testPAT, "octo", "widgets", 17, reviewPublicationMarker("run-17"))
	if err != nil || id != 101 || pages != 2 {
		t.Fatalf("marker lookup: id=%d pages=%d err=%v", id, pages, err)
	}
}

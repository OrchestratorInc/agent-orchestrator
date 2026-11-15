package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPaginationNextURLDifferentHostRejected(t *testing.T) {
	var attackerHits int32
	attackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attackerHits, 1)
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(attackerSrv.Close)

	var mainHits int32
	mainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&mainHits, 1)
		attackerURL := attackerSrv.URL + "/api/v4/projects/1/merge_requests?page=2"
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, attackerURL))
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(mainSrv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: mainSrv.URL + "/api/v4",
	})

	_, err := c.doGETPaginated(context.Background(), "/projects/1/merge_requests", nil, func([]byte) error { return nil })
	if err == nil {
		t.Fatal("doGETPaginated: error = nil, want error for Link: rel=next pointing at a different host")
	}
	if !strings.Contains(err.Error(), "pagination") && !strings.Contains(err.Error(), "host") {
		t.Errorf("doGETPaginated error = %v, want a pagination/host-validation error", err)
	}
	if got := atomic.LoadInt32(&attackerHits); got != 0 {
		t.Errorf("attacker server received %d requests; want 0 — token must not be attached to a different-host pagination URL", got)
	}
	if got := atomic.LoadInt32(&mainHits); got != 1 {
		t.Errorf("main server received %d requests; want exactly 1 (the initial page, not the rejected next page)", got)
	}
}

func TestPaginationHTTPSDowngradeRejected(t *testing.T) {
	mainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downgraded := strings.Replace(r.Host, "https://", "http://", 1)
		_ = downgraded
		host := r.Host
		nextURL := "http://" + host + r.URL.Path + "?page=2"
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(mainSrv.Close)

	restBase := strings.Replace(mainSrv.URL, "http://", "https://", 1) + "/api/v4"

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: restBase,
		HTTPClient: &http.Client{Transport: &schemeRewriteTransport{
			target: mainSrv.URL,
		}},
	})

	_, err := c.doGETPaginated(context.Background(), "/projects/1/merge_requests", nil, func([]byte) error { return nil })
	if err == nil {
		t.Fatal("doGETPaginated: error = nil, want error for HTTPS→HTTP downgrade in Link: rel=next")
	}
}

func TestPaginationPortMismatchRejected(t *testing.T) {
	var mainHits int32
	mainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&mainHits, 1)
		host := r.Host
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		nextURL := "http://" + host + ":9999/api/v4/projects/1/merge_requests?page=2"
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(mainSrv.Close)

	restBase := mainSrv.URL + "/api/v4"

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: restBase,
	})

	_, err := c.doGETPaginated(context.Background(), "/projects/1/merge_requests", nil, func([]byte) error { return nil })
	if err == nil {
		t.Fatal("doGETPaginated: error = nil, want error for port mismatch in Link: rel=next")
	}
	if got := atomic.LoadInt32(&mainHits); got != 1 {
		t.Errorf("main server received %d requests; want exactly 1 (the initial page)", got)
	}
}

func TestPaginationSameHostHappyPath(t *testing.T) {
	var pages []string
	var mainSrv *httptest.Server
	mainSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			nextURL := mainSrv.URL + "/api/v4/projects/1/merge_requests?page=2"
			w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
			fmt.Fprint(w, `[{"iid":1}]`)
			return
		}
		fmt.Fprint(w, `[{"iid":2}]`)
	}))
	t.Cleanup(mainSrv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: mainSrv.URL + "/api/v4",
	})

	var totalItems int
	_, err := c.doGETPaginated(context.Background(), "/projects/1/merge_requests", nil, func(body []byte) error {
		var items []struct {
			IID int `json:"iid"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return err
		}
		totalItems += len(items)
		return nil
	})
	if err != nil {
		t.Fatalf("doGETPaginated happy path: %v", err)
	}
	if totalItems != 2 {
		t.Errorf("total items = %d, want 2 (both pages should be fetched)", totalItems)
	}
	if len(pages) != 2 {
		t.Errorf("pages fetched = %v, want [1 2] (two pages)", pages)
	}
}

func TestPaginationDifferentAPIBaseRejected(t *testing.T) {
	var mainSrv *httptest.Server
	mainSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextURL := mainSrv.URL + "/api/v5/projects/1/merge_requests?page=2"
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(mainSrv.Close)

	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("secret-token"),
		RESTBase: mainSrv.URL + "/api/v4",
	})

	_, err := c.doGETPaginated(context.Background(), "/projects/1/merge_requests", nil, func([]byte) error { return nil })
	if err == nil {
		t.Fatal("doGETPaginated: error = nil, want error for different API base path in Link: rel=next")
	}
}

type schemeRewriteTransport struct {
	target string
}

func (t *schemeRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = target.Scheme
	req2.URL.Host = target.Host
	req2.Host = target.Host
	return http.DefaultTransport.RoundTrip(req2)
}

package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

func main() {
	addr := os.Args[1]
	upstream, _ := url.Parse("https://api.anthropic.com")
	var until atomic.Int64
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	base := proxy.Director
	proxy.Director = func(r *http.Request) { base(r); r.Host = upstream.Host }
	proxy.FlushInterval = -1
	http.HandleFunc("/__lab/limit", func(w http.ResponseWriter, r *http.Request) {
		v, _ := strconv.ParseInt(r.URL.Query().Get("until"), 10, 64)
		until.Store(v)
		log.Printf("LAB limit until=%d (%s)", v, time.Unix(v, 0).UTC().Format(time.RFC3339))
		fmt.Fprintf(w, "until=%d\n", v)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reset := until.Load()
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" && time.Now().Unix() < reset {
			h := w.Header()
			h.Set("content-type", "application/json")
			h.Set("anthropic-ratelimit-unified-status", "rejected")
			h.Set("anthropic-ratelimit-unified-reset", strconv.FormatInt(reset, 10))
			h.Set("anthropic-ratelimit-unified-representative-claim", "five_hour")
			h.Set("anthropic-ratelimit-unified-5h-status", "rejected")
			h.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset, 10))
			h.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
			h.Set("anthropic-ratelimit-unified-overage-status", "rejected")
			h.Set("anthropic-ratelimit-unified-overage-disabled-reason", "org_level_disabled")
			h.Set("request-id", "req_lab_usage_limit")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}`)
			log.Printf("LAB 429 %s %s reset=%d", r.Method, r.URL.Path, reset)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		proxy.ServeHTTP(rec, r)
		log.Printf("PASS %d %s %s", rec.code, r.Method, r.URL.Path)
	})
	log.Fatal(http.ListenAndServe(addr, nil))
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) { s.code = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

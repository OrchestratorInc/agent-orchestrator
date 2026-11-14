package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestHTTPRemoteBrowserForwardsWithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent" || r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct {
			Args  []string          `json:"args"`
			Files map[string]string `json:"files"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if !slices.Equal(in.Args, []string{"click", "@e2"}) || in.Files["/tmp/a"] != "QQ==" {
			t.Errorf("request = %+v", in)
		}
		_ = json.NewEncoder(w).Encode(RemoteResponse{Output: "ok", Files: map[string]string{"/tmp/s.png": "cG5n"}})
	}))
	defer srv.Close()
	out, err := NewHTTPRemoteBrowser(srv.URL+"/", "secret").Forward(context.Background(), []string{"click", "@e2"}, map[string]string{"/tmp/a": "QQ=="})
	if err != nil || out.Output != "ok" || out.Files["/tmp/s.png"] != "cG5n" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if _, err := NewHTTPRemoteBrowser(srv.URL, "wrong").Forward(context.Background(), []string{"snapshot"}, nil); err == nil {
		t.Fatal("wrong token accepted")
	}
}

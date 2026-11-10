package caller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareKeepsOnlyABoundedCaller(t *testing.T) {
	for _, tc := range []struct{ name, header, want string }{
		{name: "agent", header: "proj-1", want: "proj-1"},
		{name: "none", header: "", want: ""},
		{name: "oversize", header: strings.Repeat("x", maxLen+1), want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			handler := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = From(r.Context()) }))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
			if tc.header != "" {
				req.Header.Set(Header, tc.header)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("caller = %q, want %q", got, tc.want)
			}
		})
	}
}

package httpd

import (
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestRemoteJSONCompressionNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		accept  []string
		encoded string
	}{
		{"absent", nil, ""},
		{"empty", []string{""}, ""},
		{"gzip", []string{"gzip"}, "gzip"},
		{"gzip_case_and_whitespace", []string{" GZip ; Q=1.000 "}, "gzip"},
		{"gzip_positive_fraction", []string{"gzip;q=0.001"}, "gzip"},
		{"gzip_one_decimal_point", []string{"gzip;q=1."}, "gzip"},
		{"gzip_zero", []string{"gzip;q=0"}, ""},
		{"gzip_zero_fraction", []string{"gzip;q=0.000"}, ""},
		{"gzip_zero_identity", []string{"gzip;q=0, identity;q=1"}, ""},
		{"gzip_zero_deflate", []string{"gzip;q=0, deflate;q=1"}, "deflate"},
		{"deflate", []string{"deflate"}, "deflate"},
		{"deflate_zero", []string{"deflate;q=0"}, ""},
		{"higher_deflate_quality", []string{"gzip;q=0.2, deflate;q=0.8"}, "deflate"},
		{"higher_gzip_quality", []string{"deflate;q=0.2, gzip;q=0.8"}, "gzip"},
		{"equal_quality_prefers_gzip", []string{"deflate, gzip"}, "gzip"},
		{"explicit_identity_preferred", []string{"gzip;q=0.5, identity;q=1"}, ""},
		{"explicit_identity_lower", []string{"gzip;q=1, identity;q=0.5"}, "gzip"},
		{"other_encoding", []string{"br"}, ""},
		{"encoding_substring", []string{"xgzip"}, ""},
		{"wildcard", []string{"*"}, "gzip"},
		{"wildcard_zero", []string{"*;q=0"}, ""},
		{"wildcard_with_gzip_rejected", []string{"gzip;q=0, *;q=1"}, "deflate"},
		{"wildcard_with_both_rejected", []string{"gzip;q=0, deflate;q=0, *;q=1"}, ""},
		{"wildcard_does_not_override_explicit", []string{"gzip;q=1, *;q=0"}, "gzip"},
		{"multiple_field_lines", []string{"gzip;q=0", "deflate;q=1"}, "deflate"},
		{"duplicate_rejection_first", []string{"gzip;q=0", "gzip;q=1"}, ""},
		{"duplicate_rejection_last", []string{"gzip;q=1, gzip;q=0"}, ""},
		{"malformed_weight", []string{"gzip;q=invalid"}, ""},
		{"missing_weight", []string{"gzip;q="}, ""},
		{"negative_weight", []string{"gzip;q=-1"}, ""},
		{"over_one", []string{"gzip;q=1.001"}, ""},
		{"over_precision", []string{"gzip;q=0.0001"}, ""},
		{"quoted_weight", []string{`gzip;q="0"`}, ""},
		{"nan_weight", []string{"gzip;q=NaN"}, ""},
		{"unknown_parameter", []string{"gzip;level=1"}, ""},
		{"duplicate_parameter", []string{"gzip;q=1;q=0"}, ""},
		{"malformed_specific_overrides_wildcard", []string{"gzip;q=invalid, deflate;q=0, *;q=1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat(`{"id":"scratch-session"}`, 200)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			for _, field := range tc.accept {
				req.Header.Add("Accept-Encoding", field)
			}
			handler := remoteJSONCompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r != req || !reflect.DeepEqual(r.Header.Values("Accept-Encoding"), tc.accept) {
					t.Fatal("compression changed the request seen by the handler")
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				_, _ = io.WriteString(w, body)
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if got := recorder.Header().Get("Content-Encoding"); got != tc.encoded {
				t.Fatalf("Content-Encoding=%q, want %q", got, tc.encoded)
			}
			if !reflect.DeepEqual(req.Header.Values("Accept-Encoding"), tc.accept) {
				t.Fatal("compression mutated the original request headers")
			}
			var decoded io.Reader = recorder.Body
			switch tc.encoded {
			case "gzip":
				reader, err := gzip.NewReader(recorder.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				decoded = reader
			case "deflate":
				reader, err := zlib.NewReader(recorder.Body)
				if err != nil {
					t.Fatalf("HTTP deflate response lacks valid zlib framing: %v", err)
				}
				defer reader.Close()
				decoded = reader
			}
			got, err := io.ReadAll(decoded)
			if err != nil || string(got) != body {
				t.Fatalf("response did not round-trip: %v", err)
			}
			if tc.encoded != "" && recorder.Header().Get("Content-Length") != "" {
				t.Fatal("compressed response retained the original Content-Length")
			}
		})
	}
}

func TestRemoteJSONCompressionPreservesExistingEncoding(t *testing.T) {
	body := "already-encoded-response"
	handler := remoteJSONCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	req := httptest.NewRequest(http.MethodGet, "/preview", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Header().Get("Content-Encoding") != "br" || recorder.Body.String() != body || recorder.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatal("an existing encoded response was changed")
	}
}

func TestRemoteJSONCompressionFlushesEventStreams(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := remoteJSONCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		if !recorder.Flushed || recorder.Body.String() != "data: first\n\n" {
			t.Fatal("event stream was buffered until the handler returned")
		}
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(recorder, req)
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Body.String() != "data: first\n\ndata: second\n\n" {
		t.Fatal("event stream was encoded or changed")
	}
}

func TestRemoteJSONCompressionPreservesPartialResponses(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate"} {
		t.Run(encoding, func(t *testing.T) {
			// A 206 without a Range request still must bypass compression.
			handler := remoteJSONCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Range", "bytes 0-3/10")
				w.Header().Set("Content-Length", "4")
				w.Header().Set("ETag", `"original"`)
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.WriteString(w, "part")
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Fatal(err)
				}
			}))
			req := httptest.NewRequest(http.MethodGet, "/file.json", nil)
			req.Header.Set("Accept-Encoding", encoding)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "part" || !recorder.Flushed {
				t.Fatalf("partial response changed: status=%d body=%q flushed=%v", recorder.Code, recorder.Body.String(), recorder.Flushed)
			}
			for name, want := range map[string]string{"Content-Encoding": "", "Content-Range": "bytes 0-3/10", "Content-Length": "4", "ETag": `"original"`} {
				if got := recorder.Header().Get(name); got != want {
					t.Errorf("%s=%q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestRemoteJSONCompressionValidators(t *testing.T) {
	for _, tc := range []struct {
		name, accept, contentType, existingEncoding, etag, wantETag string
	}{
		{"gzip_strong", "gzip", "application/json", "", `"original"`, `W/"original"`},
		{"deflate_strong", "deflate", "application/json; charset=utf-8", "", `"original"`, `W/"original"`},
		{"already_weak", "gzip", "application/json", "", `W/"original"`, `W/"original"`},
		{"no_validator", "gzip", "application/json", "", "", ""},
		{"identity", "gzip;q=0", "application/json", "", `"original"`, `"original"`},
		{"existing_encoding", "gzip", "application/json", "br", `"original"`, `"original"`},
		{"other_content_type", "gzip", "text/plain", "", `"original"`, `"original"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := remoteJSONCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.existingEncoding != "" {
					w.Header().Set("Content-Encoding", tc.existingEncoding)
				}
				if tc.etag != "" {
					w.Header().Set("ETag", tc.etag)
				}
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			req := httptest.NewRequest(http.MethodGet, "/file.json", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if got := recorder.Result().Header.Get("ETag"); got != tc.wantETag {
				t.Fatalf("ETag=%q, want %q", got, tc.wantETag)
			}
		})
	}
}

func TestRemoteJSONCompressionNotModified(t *testing.T) {
	for _, tc := range []struct {
		name, accept, contentType, existingEncoding, etag, wantETag string
	}{
		{"gzip_without_type", "gzip", "", "", `"v1"`, `W/"v1"`},
		{"deflate_without_type", "deflate", "", "", `"v1"`, `W/"v1"`},
		{"json_type_retained", "gzip", "application/json", "", `"v1"`, `W/"v1"`},
		{"already_weak", "gzip", "", "", `W/"v1"`, `W/"v1"`},
		{"no_etag", "gzip", "", "", "", ""},
		{"identity", "identity", "", "", `"v1"`, `"v1"`},
		{"no_negotiation", "", "", "", `"v1"`, `"v1"`},
		{"gzip_rejected", "gzip;q=0", "", "", `"v1"`, `"v1"`},
		{"known_non_json", "gzip", "text/plain", "", `"v1"`, `"v1"`},
		{"existing_encoding", "gzip", "", "br", `"v1"`, `"v1"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := remoteJSONCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Content-Encoding", tc.existingEncoding)
				w.Header().Set("ETag", tc.etag)
				w.WriteHeader(http.StatusNotModified)
			}))
			req := httptest.NewRequest(http.MethodGet, "/file.json", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusNotModified || recorder.Body.Len() != 0 {
				t.Fatalf("304 acquired a body: status=%d bytes=%d", recorder.Code, recorder.Body.Len())
			}
			for name, want := range map[string]string{"ETag": tc.wantETag, "Content-Encoding": tc.existingEncoding} {
				if got := recorder.Result().Header.Get(name); got != want {
					t.Errorf("%s=%q, want %q", name, got, want)
				}
			}
		})
	}
}

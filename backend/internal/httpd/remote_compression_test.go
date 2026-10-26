package httpd

import (
	"compress/flate"
	"compress/gzip"
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
				reader := flate.NewReader(recorder.Body)
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

package controllers

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHasCacheDirective(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   bool
	}{
		{"missing", nil, false},
		{"exact", []string{"no-transform"}, true},
		{"comma separated and case insensitive", []string{"private, NO-TRANSFORM"}, true},
		{"separate headers", []string{"private", "no-transform"}, true},
		{"quoted directive argument", []string{`no-cache="Set-Cookie, no-transform, ETag"`}, false},
		{"escaped quote in argument", []string{`no-cache="Set-Cookie, \"no-transform\", ETag"`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasCacheDirective(tc.values, "no-transform"); got != tc.want {
				t.Fatalf("hasCacheDirective(%q, no-transform) = %t, want %t", tc.values, got, tc.want)
			}
		})
	}
}

func TestIsHTMLMediaType(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		want        bool
	}{
		{"text/html", true},
		{"TEXT/HTML; charset=utf-8", true},
		{"application/xhtml+xml", true},
		{"text/html; charset", true},
		{"text/html; charset=UTF-8; charset=utf-8", true},
		{" Application/XHTML+XML ; charset=utf-8", true},
		{"text/plain; charset=utf-8", false},
		{"text/css", false},
		{"", false},
		{"not a media type", false},
	} {
		if got := isHTMLMediaType(tc.contentType); got != tc.want {
			t.Errorf("isHTMLMediaType(%q) = %t, want %t", tc.contentType, got, tc.want)
		}
	}
}

// previewServedContentType follows http.ServeContent: a preset header, then the
// name's extension, then a sniff that leaves the content rewound for serving.
func TestPreviewServedContentType(t *testing.T) {
	const page = `<!doctype html><a href="mailto:support@example.test">support@example.test</a>`
	for _, tc := range []struct {
		name, file, preset, want string
	}{
		{name: "preset header wins", file: "page.html", preset: "text/plain", want: "text/plain"},
		{name: "extension", file: "page.html", want: "text/html; charset=utf-8"},
		{name: "sniffed html", file: "page", want: "text/html; charset=utf-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.preset != "" {
				header.Set("Content-Type", tc.preset)
			}
			content := strings.NewReader(page)
			got, err := previewServedContentType(header, tc.file, content)
			if err != nil || got != tc.want {
				t.Fatalf("previewServedContentType = %q, %v; want %q", got, err, tc.want)
			}
			rest, err := io.ReadAll(content)
			if err != nil || string(rest) != page {
				t.Fatalf("content after classification = %q, %v; want it rewound", rest, err)
			}
		})
	}
}

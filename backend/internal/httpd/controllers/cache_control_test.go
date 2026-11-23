package controllers

import "testing"

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

package daemon

import "testing"

func TestPublishedSentryDSNOnlyForStableAndNightly(t *testing.T) {
	for version, want := range map[string]string{
		"0.14.10":                           "dsn",
		"0.14.11-nightly.202611100625":      "dsn",
		"0.14.11-edge.1":                    "",
		"0.14.11-pr.42":                     "",
		"0.13.0-ragcorp.7":                  "",
		"0.14.10+ptbr.1":                    "",
		"0.14.9-nightly.202610311751.local": "",
		"":                                  "",
	} {
		if got := publishedSentryDSN("dsn", version); got != want {
			t.Fatalf("publishedSentryDSN(%q) = %q, want %q", version, got, want)
		}
	}
}

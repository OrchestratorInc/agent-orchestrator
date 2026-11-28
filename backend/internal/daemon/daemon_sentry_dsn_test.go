package daemon

import "testing"

func TestPublishedSentryDSNOnlyForStableRCAndNightly(t *testing.T) {
	for version, want := range map[string]string{
		"0.14.10":                           "dsn",
		"0.14.11-nightly.202611100625":      "dsn",
		"0.15.8":                            "dsn",
		"0.15.8-nightly.202611281200":       "dsn",
		"0.15.8-rc.1":                       "dsn",
		"0.15.8-rc.12":                      "dsn",
		" 0.15.8-rc.1\n":                    "dsn",
		"0.15.8-rc":                         "",
		"0.15.8-rc.x":                       "",
		"0.15.8-rc.1.202611281200":          "",
		"0.15.8-pr123.1":                    "",
		"0.15.8+fork.1":                     "",
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

// A release candidate reports exactly like a stable build; only its version
// string tells the two apart.
func TestSentryEnvironmentTreatsReleaseCandidateAsStable(t *testing.T) {
	for version, want := range map[string]string{
		"0.15.8":                      "stable",
		"0.15.8-rc.1":                 "stable",
		"0.15.8-rc.12":                "stable",
		"0.15.8-nightly.202611281200": "nightly",
		"0.15.8-pr123.1":              "development",
		"":                            "unknown",
	} {
		if got := sentryEnvironment(version); got != want {
			t.Fatalf("sentryEnvironment(%q) = %q, want %q", version, got, want)
		}
	}
}

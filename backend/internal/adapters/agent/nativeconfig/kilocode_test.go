package nativeconfig

import (
	"net/url"
	"testing"
)

func TestKiloReadOnlyDatabaseURIPreservesLiteralPaths(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"/data/team%20#root/kilo.db", "/data/team%20#root/kilo.db"},
		{"C:/Users/alice/team%20#root/kilo.db", "/C:/Users/alice/team%20#root/kilo.db"},
	} {
		t.Run(test.path, func(t *testing.T) {
			raw := KiloReadOnlyDatabaseURI(test.path)
			uri, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if uri.Scheme != "file" || uri.Host != "" || uri.Path != test.want || uri.Fragment != "" || uri.Query().Get("mode") != "ro" || uri.Query().Get("_pragma") != "busy_timeout(2000)" {
				t.Fatalf("unsafe database URI: %q (%+v)", raw, uri)
			}
		})
	}
}

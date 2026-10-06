package chat

import (
	"strings"
	"testing"
)

func TestInjectRenderBootstrapLandsAfterTheDoctype(t *testing.T) {
	cases := map[string]struct{ page, wantPrefix, wantSuffix string }{
		"doctype": {
			page:       "<!DOCTYPE html><html lang=\"en\"><head><title>x</title></head><body>b</body></html>",
			wantPrefix: "<!DOCTYPE html><meta name=\"viewport\"",
			wantSuffix: "<html lang=\"en\"><head><title>x</title></head><body>b</body></html>",
		},
		"bom and comment before doctype": {
			page:       "\xef\xbb\xbf<!-- made by an agent -->\n<!doctype html><p>x</p>",
			wantPrefix: "\xef\xbb\xbf<!-- made by an agent -->\n<!doctype html><meta name=\"viewport\"",
			wantSuffix: "<p>x</p>",
		},
		"no doctype": {
			page:       "<div>x</div>",
			wantPrefix: "<!doctype html><meta name=\"viewport\"",
			wantSuffix: "<div>x</div>",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := injectRenderBootstrap(tc.page)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("prefix = %.160q", got)
			}
			if !strings.HasSuffix(got, tc.wantSuffix) {
				t.Fatalf("page body not preserved verbatim: %.160q", got[len(got)-min(len(got), 160):])
			}
			for _, want := range []string{`<style id="ao-theme">`, "ui/notifications/size-changed", "ui/open-link", "ao-theme="} {
				if !strings.Contains(got, want) {
					t.Errorf("bootstrap missing %q", want)
				}
			}
		})
	}
}

func TestInjectRenderBootstrapKeepsThePagesViewport(t *testing.T) {
	got := injectRenderBootstrap(`<!doctype html><meta name="viewport" content="width=500">`)
	if n := strings.Count(got, `name="viewport"`); n != 1 {
		t.Fatalf("viewport metas = %d, want the page's own only", n)
	}
}

func TestRenderBootstrapCannotCloseItsOwnElements(t *testing.T) {
	if strings.Contains(strings.ToLower(renderBootstrapJS), "</script") {
		t.Fatal("bootstrap script would end its own <script> element")
	}
	if strings.Contains(strings.ToLower(renderDefaultThemeCSS+renderBaseCSS), "</style") {
		t.Fatal("bootstrap CSS would end its own <style> element")
	}
}

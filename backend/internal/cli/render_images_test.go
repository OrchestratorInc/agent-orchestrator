package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const pngSignature = "\x89PNG\r\n\x1a\n"

func writeImage(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// sparseImage is a PNG of the given size that takes no disk space.
func sparseImage(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := writeImage(t, dir, name, pngSignature)
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindLocalImages(t *testing.T) {
	atLimit := "/" + strings.Repeat("a", 2043) + ".png"
	cases := []struct {
		name string
		html string
		want []string
	}{
		{"double quotes", `<img src="/tmp/a.png">`, []string{"/tmp/a.png"}},
		{"single quotes", `<img src='/tmp/a.JPG'>`, []string{"/tmp/a.JPG"}},
		{"backticks", "const s = `/tmp/a b.webp`;", []string{"/tmp/a b.webp"}},
		{"css url", `div{background:url(/tmp/bg.svg)}`, []string{"/tmp/bg.svg"}},
		{"css url with spaces", `div{background:url( /tmp/bg.gif )}`, []string{"/tmp/bg.gif"}},
		{"uppercase css url", `div{background:URL(/tmp/bg.AVIF)}`, []string{"/tmp/bg.AVIF"}},
		{"windows path in a JS string", `const p = "C:\\shots\\a.png";`, []string{`C:\\shots\\a.png`}},
		{"windows path with forward slashes", `<img src="c:/shots/a.ico">`, []string{"c:/shots/a.ico"}},
		{"several on a line", `<img src="/a.png"><img src='/b.jpeg'>`, []string{"/a.png", "/b.jpeg"}},
		{"a file at the root", `<img src="/.bmp">`, []string{"/.bmp"}},
		{"2048 characters", `"` + atLimit + `"`, []string{atLimit}},
		{"https url", `<img src="https://example.com/a.png">`, nil},
		{"protocol-relative url", `<img src="//cdn.example.com/a.png">`, nil},
		{"protocol-relative css url", `div{background:url(//cdn.example.com/a.png)}`, nil},
		{"data uri", `<img src="data:image/png;base64,AAAA">`, nil},
		{"blob url", `<img src="blob:https://example.com/a.png">`, nil},
		{"bare file name", `<img src="a.png">`, nil},
		{"relative path", `<img src="./a.png">`, nil},
		{"not an image", `<a href="/tmp/a.txt">`, nil},
		{"query string", `<img src="/tmp/a.png?v=1">`, nil},
		{"crosses a line", "<img src=\"/tmp/a\nb.png\">", nil},
		{"mismatched quotes", `<img src="/tmp/a.png'>`, nil},
		{"unquoted path in prose", `<p>see /tmp/a.png</p>`, nil},
		{"over 2048 characters", `"/a` + atLimit + `"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, ref := range findLocalImages(tc.html) {
				if tc.html[ref.start:ref.end] != ref.path {
					t.Fatalf("span %d-%d is %q, not %q", ref.start, ref.end, tc.html[ref.start:ref.end], ref.path)
				}
				got = append(got, ref.path)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	// Inside a JS string a Windows path's backslashes are escaped.
	if got := imageFilePath(`C:\\shots\\a.png`); got != `C:\shots\a.png` {
		t.Fatalf("windows file path = %q", got)
	}
	if got := imageFilePath(`/tmp/a\\b.png`); got != `/tmp/a\\b.png` {
		t.Fatalf("posix file path = %q", got)
	}
}

func TestIsImageBytes(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"png", pngSignature, true},
		{"jpeg", "\xff\xd8\xff\xe0\x00\x10JFIF", true},
		{"gif", "GIF89a\x01\x00", true},
		{"ico", "\x00\x00\x01\x00\x01\x00", true},
		{"bmp", "BM\x46\x00\x00\x00\x00\x00\x00\x00\x36\x00", true},
		{"webp", "RIFF\x24\x00\x00\x00WEBPVP8 ", true},
		{"avif", "\x00\x00\x00\x1cftypavif", true},
		{"avif sequence", "\x00\x00\x00\x1cftypavis", true},
		{"heif", "\x00\x00\x00\x1cftypmif1", true},
		{"svg", "<svg/>", true},
		{"svg after an xml declaration", "<?xml version=\"1.0\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>", true},
		{"svg after a comment", "<!-- exported -->\n<svg width=\"1\"></svg>", true},
		{"svg after a doctype with an internal subset", strings.Join([]string{
			`<?xml version="1.0"?>`,
			`<?xml-stylesheet href="theme.css"?>`,
			`<!DOCTYPE svg [ <!ENTITY fill "red"> <!-- a ] comment --> ]>`,
			`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"/>`,
		}, "\n"), true},
		{"svg after a byte order mark", "\ufeff<svg/>", true},
		{"text named like an image", "API_KEY=abc123", false},
		{"empty", "", false},
		{"bmp without its reserved zeros", "BM\x46\x00\x00\x00\x01\x00\x00\x00", false},
		{"riff that is not webp", "RIFF\x24\x00\x00\x00WAVEfmt ", false},
		{"mp4", "\x00\x00\x00\x1cftypisom", false},
		{"html with an svg inside", "<!doctype html><body><svg></svg>API_KEY=abc123", false},
		{"svg inside a quoted entity", `<!DOCTYPE config [<!ENTITY a "a"><!ENTITY b "]><svg/>">]><config>API_KEY=abc123</config>`, false},
		{"processing instructions before another root", strings.Repeat("<?p?>", 40) + "<config><svg/></config>", false},
		{"unclosed doctype", `<!DOCTYPE svg [<!ENTITY a "x><svg/>`, false},
		{"unclosed comment", "<!-- <svg/>", false},
		{"uppercase root", "<SVG/>API_KEY=abc123", false},
		{"longer root name", "<svg\u00e9/>API_KEY=abc123", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isImageBytes([]byte(tc.data)); got != tc.want {
				t.Fatalf("isImageBytes(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

func TestInlineLocalImagesReplacesReferencesWithDataURIs(t *testing.T) {
	dir := t.TempDir()
	png := writeImage(t, dir, "shot.png", pngSignature)
	svg := writeImage(t, dir, "logo.SVG", "<svg/>")
	jpeg := writeImage(t, dir, "photo.jpeg", "\xff\xd8\xff\xe0")
	page := func(png, svg, jpeg string) string {
		return `<img src="` + png + `"><div style="background:url(` + svg + `)"></div>` +
			"<script>const shots = ['" + png + "', `" + jpeg + "`];</script>" +
			`<img src="https://example.com/a.png"><img src="//cdn.example.com/b.png"><img src="./c.png">` +
			`<img src="data:image/png;base64,AAAA">`
	}
	encode := func(data string) string { return base64.StdEncoding.EncodeToString([]byte(data)) }

	got, missing, err := inlineLocalImages(page(png, svg, jpeg))
	if err != nil || len(missing) != 0 {
		t.Fatalf("err=%v missing=%q", err, missing)
	}
	want := page("data:image/png;base64,"+encode(pngSignature), "data:image/svg+xml;base64,"+encode("<svg/>"),
		"data:image/jpeg;base64,"+encode("\xff\xd8\xff\xe0"))
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestInlineLocalImagesReadsADuplicateOnce(t *testing.T) {
	png := writeImage(t, t.TempDir(), "shot.png", pngSignature)
	opens := 0
	open := openRenderImage
	openRenderImage = func(name string) (*os.File, error) {
		opens++
		return open(name)
	}
	t.Cleanup(func() { openRenderImage = open })

	got, _, err := inlineLocalImages(`<img src="` + png + `"><img src='` + png + `'>`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<img src="data:image/png;base64,iVBORw0KGgo="><img src='data:image/png;base64,iVBORw0KGgo='>`; got != want || opens != 1 {
		t.Fatalf("opens=%d got %s", opens, got)
	}
}

func TestInlineLocalImagesLeavesUnreadableReferencesAsWritten(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "missing.jpg")
	folder := filepath.Join(dir, "folder.png")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	// Named like an image, but a renamed file or symlink must not carry other data.
	secret := writeImage(t, dir, "secret.png", "API_KEY=abc123")
	report := writeImage(t, dir, "report.svg", "<!doctype html><body><svg></svg>API_KEY=abc123")
	html := `<img src="` + gone + `"><img src='` + folder + `'><script>const p = "C:\\nope\\shot.webp";</script>` +
		`<img src="` + secret + `"><img src="` + report + `"><img src="` + gone + `">`

	got, missing, err := inlineLocalImages(html)
	if err != nil {
		t.Fatal(err)
	}
	if got != html {
		t.Fatalf("page changed: %s", got)
	}
	if want := []string{gone, folder, `C:\\nope\\shot.webp`, secret, report}; !slices.Equal(missing, want) {
		t.Fatalf("missing = %q, want %q", missing, want)
	}
}

func TestInlineLocalImagesEnforcesSizeLimits(t *testing.T) {
	dir := t.TempDir()
	big := sparseImage(t, dir, "big.png", 10<<20+1)
	seven := sparseImage(t, dir, "seven.png", 7<<20)
	cases := []struct {
		name string
		html string
		want string
	}{
		{"an image over 10 MiB", `<img src="` + big + `">`,
			big + " is 10.0 MiB; each local image must be at most 10.0 MiB."},
		{"a page over 25 MiB", strings.Repeat(`<img src="`+seven+`">`, 3),
			"With its images inlined the page is 28.0 MiB; the limit is 25.0 MiB. Use smaller images."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := inlineLocalImages(tc.html)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// stubOpenRenderImage routes inlineLocalImages' reads through open.
func stubOpenRenderImage(t *testing.T, open func(string) (*os.File, error)) {
	t.Helper()
	original := openRenderImage
	openRenderImage = open
	t.Cleanup(func() { openRenderImage = original })
}

func TestInlineLocalImagesRefusesAnImageThatGrowsAfterTheStat(t *testing.T) {
	png := writeImage(t, t.TempDir(), "grows.png", pngSignature)
	open := openRenderImage
	stubOpenRenderImage(t, func(name string) (*os.File, error) {
		if err := os.Truncate(name, 200<<20); err != nil {
			t.Fatal(err)
		}
		return open(name)
	})

	_, _, err := inlineLocalImages(`<img src="` + png + `">`)
	if want := png + " is over 10.0 MiB; each local image must be at most 10.0 MiB."; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestInlineLocalImagesReportsASymlinkToANonImageAsMissing(t *testing.T) {
	dir := t.TempDir()
	secret := writeImage(t, dir, "secret.txt", "API_KEY=abc123")
	link := filepath.Join(dir, "shot.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	opens := 0
	open := openRenderImage
	stubOpenRenderImage(t, func(name string) (*os.File, error) {
		opens++
		return open(name)
	})

	html := `<img src="` + link + `">`
	got, missing, err := inlineLocalImages(html)
	if err != nil || got != html || !slices.Equal(missing, []string{link}) || opens != 1 {
		t.Fatalf("err=%v opens=%d missing=%q page=%s", err, opens, missing, got)
	}
}

func TestDataURIPrefixFallsBackToOctetStream(t *testing.T) {
	if got := dataURIPrefix("/a.PNG"); got != "data:image/png;base64," {
		t.Fatalf("png = %q", got)
	}
	if got := dataURIPrefix("/a.tiff"); got != "data:application/octet-stream;base64," {
		t.Fatalf("unknown = %q", got)
	}
}

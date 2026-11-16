package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	pngSignature = "\x89PNG\r\n\x1a\n"
	jpegHeader   = "\xff\xd8\xff\xdb\x00\x43\x00"
	// bmpHeader is a file header and the start of a 40-byte BITMAPINFOHEADER.
	bmpHeader = "BM\x3a\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00\x28\x00\x00\x00\x01\x00"
)

// ftyp builds an ISO BMFF ftyp box with a zero minor version.
func ftyp(major string, compatible ...string) string {
	body := "ftyp" + major + "\x00\x00\x00\x00" + strings.Join(compatible, "")
	size := len(body) + 4
	return string([]byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}) + body
}

func writeImage(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// bigPNG writes a sparse PNG of the given size, so it costs no disk space.
func bigPNG(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := writeImage(t, dir, name, pngSignature)
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	return path
}

func wrapOpenRenderImage(t *testing.T, wrap func(name string, open func(string) (*os.File, error)) (*os.File, error)) {
	t.Helper()
	original := openRenderImage
	openRenderImage = func(name string) (*os.File, error) { return wrap(name, original) }
	t.Cleanup(func() { openRenderImage = original })
}

func TestFindLocalImages(t *testing.T) {
	longest := "/" + strings.Repeat("x", maxImagePathChars-5) + ".gif"
	for _, tc := range []struct {
		name string
		html string
		want []string
	}{
		{"img src", `<img alt="chart" src="/home/me/chart.png">`, []string{"/home/me/chart.png"}},
		{"single-quoted, upper-case extension", `<img src='/srv/out/Logo.JPEG'>`, []string{"/srv/out/Logo.JPEG"}},
		{"template literal with a space", "img.src = `/Users/me/My Shots/one.webp`;", []string{"/Users/me/My Shots/one.webp"}},
		{"css url", `body { background: url(/opt/tiles/grid.svg) }`, []string{"/opt/tiles/grid.svg"}},
		{"css url with padding", "li { list-style: url(\t/opt/dot.ico ) }", []string{"/opt/dot.ico"}},
		{"upper-case css url", `<div style="background:Url(/opt/bg.avif)">`, []string{"/opt/bg.avif"}},
		{"escaped windows path", `fetch("D:\\renders\\frame.bmp")`, []string{`D:\\renders\\frame.bmp`}},
		{"windows path with slashes", `<img src="e:/renders/frame.jpg">`, []string{"e:/renders/frame.jpg"}},
		{"two on one line", `<img src="/a/1.png"><img src='/a/2.gif'>`, []string{"/a/1.png", "/a/2.gif"}},
		{"after a non-image string", `<a title="x" href="/a/b.html"><img src="/a/c.png"></a>`, []string{"/a/c.png"}},
		{"hidden file at the root", `<img src="/.png">`, []string{"/.png"}},
		{"longest path", "'" + longest + "'", []string{longest}},
		{"path too long", "'/" + longest + "'", nil},
		{"https", `<img src="https://example.org/a.png">`, nil},
		{"protocol-relative", `<img src="//static.example.org/a.png">`, nil},
		{"protocol-relative css url", `div { background: url(//static.example.org/a.png) }`, nil},
		{"data uri", `<img src="data:image/gif;base64,R0lGOD">`, nil},
		{"blob", `<img src="blob:null/1234.png">`, nil},
		{"relative", `<img src="img/a.png"><img src="../a.png">`, nil},
		{"other extension", `<script src="/srv/app.js"></script>`, nil},
		{"no extension", `<img src="/srv/png">`, nil},
		{"directory named like an image", `<img src="/srv/a.png/b">`, nil},
		{"with a fragment", `<img src="/srv/a.svg#icon">`, nil},
		{"split across lines", "<img src='/srv/a\r\n.png'>", nil},
		{"different closing quote", "<img src=\"/srv/a.png`>", nil},
		{"bare path in text", `<p>saved to /srv/a.png</p>`, nil},
		{"quoted css url argument", `div { background: url("/srv/a.png") }`, []string{"/srv/a.png"}},
		{"unclosed css url", `div { background: url(/srv/a.png`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, ref := range findLocalImages(tc.html) {
				if tc.html[ref.start:ref.end] != ref.path {
					t.Fatalf("span [%d:%d] holds %q, not %q", ref.start, ref.end, tc.html[ref.start:ref.end], ref.path)
				}
				got = append(got, ref.path)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("found %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageFilePathUnescapesOnlyWindowsPaths(t *testing.T) {
	if got := imageFilePath(`D:\\renders\\frame.bmp`); got != `D:\renders\frame.bmp` {
		t.Fatalf("windows: %q", got)
	}
	if got := imageFilePath(`/srv/odd\\name.png`); got != `/srv/odd\\name.png` {
		t.Fatalf("posix: %q", got)
	}
}

func TestImageType(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"png", pngSignature, "image/png"},
		{"jpeg", jpegHeader, "image/jpeg"},
		{"gif87a", "GIF87a\x02\x00\x02\x00", "image/gif"},
		{"webp", "RIFF\x1a\x00\x00\x00WEBPVP8L\x0d", "image/webp"},
		{"ico", "\x00\x00\x01\x00\x02\x00\x10\x10", "image/x-icon"},
		{"bmp", bmpHeader, "image/bmp"},
		{"avif", ftyp("avif", "mif1", "miaf"), "image/avif"},
		{"avif listed as compatible", ftyp("mif1", "avif"), "image/avif"},
		{"avif sequence", ftyp("avis", "msf1"), "image/avif"},
		{"svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"/>`, "image/svg+xml"},
		{"svg with a prolog", "\ufeff<?xml version='1.0' encoding='UTF-8'?>\r\n<!-- drawn by hand -->\n" +
			`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">` + "\n<svg/>", "image/svg+xml"},
		{"svg after an internal subset", `<!DOCTYPE svg [<!ENTITY ink "#123"> <!-- ] > --> <!ATTLIST svg a CDATA "]>">]><svg></svg>`, "image/svg+xml"},
		{"svg with entity namespaces", "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!-- Generator: Adobe Illustrator 16.0.0 -->\n" +
			`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [` +
			"\n\t<!ENTITY ns_svg \"http://www.w3.org/2000/svg\">\n\t<!ENTITY ns_xlink \"http://www.w3.org/1999/xlink\">\n]>\n" +
			`<svg version="1.1" xmlns="&ns_svg;" xmlns:xlink="&ns_xlink;" width="24" height="24"><rect width="24" height="24"/></svg>`, "image/svg+xml"},
		{"latin-1 svg with a non-ascii root attribute", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><svg aria-label=\"caf\xe9\"/>", "image/svg+xml"},
		{"windows-1252 svg with a non-ascii root attribute", "<?xml version='1.0' encoding='windows-1252'?>\n<svg xmlns=\"http://www.w3.org/2000/svg\" aria-label=\"\x93quoted\x94\"/>", "image/svg+xml"},
		{"svg declaring an unknown charset", "<?xml version=\"1.0\" encoding=\"x-made-up\"?><svg/>", "image/svg+xml"},
		{"text", "OPENAI_API_KEY=sk-test", ""},
		{"empty", "", ""},
		{"text starting with BM", "BMW_TOKEN=0123456789abcdef", ""},
		{"wave audio", "RIFF\x1a\x00\x00\x00WAVEfmt \x10\x00", ""},
		{"mp4 video", ftyp("isom", "iso2", "mp41"), ""},
		{"heic photo", ftyp("heic", "mif1", "heic"), ""},
		{"ftyp longer than the file", ftyp("avif")[:14], ""},
		{"html page holding an svg", "<!DOCTYPE html><html><body><svg/>TOKEN=1</body></html>", ""},
		{"svg tag inside a doctype string", `<!DOCTYPE doc [<!ENTITY e "]><svg/>">]><doc>TOKEN=1</doc>`, ""},
		{"text before the root", "TOKEN=1\n<svg/>", ""},
		{"prefixed root", `<s:svg xmlns:s="http://www.w3.org/2000/svg"/>`, ""},
		{"upper-case root", "<SVG/>", ""},
		{"root that only starts with svg", "<svgx/>", ""},
		{"unterminated comment", "<!-- <svg/>", ""},
		{"unterminated doctype", `<!DOCTYPE svg [<!ENTITY e "><svg/>`, ""},
		{"latin-1 document with another root", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<config>TOKEN=1</config>", ""},
		{"entity text before the root", `<!DOCTYPE svg [<!ENTITY t "TOKEN=1">]>&t;<svg/>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := imageType([]byte(tc.data))
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("imageType(%q) = %q, %v; want %q", tc.data, got, ok, tc.want)
			}
		})
	}
}

func TestInlineLocalImages(t *testing.T) {
	dir := t.TempDir()
	chart := writeImage(t, dir, "chart.PNG", pngSignature)
	icon := writeImage(t, dir, "icon.svg", "<svg/>")
	photo := writeImage(t, dir, "photo.png", jpegHeader) // a JPEG saved as .png
	page := func(chart, icon, photo string) string {
		return `<h1>Report</h1><img src="` + chart + `"><i style="background: url(` + icon + `)"></i>` +
			"<script>const frames = ['" + chart + "', `" + photo + "`];</script>" +
			`<img src="https://example.org/a.png"><img src="//example.org/b.png"><img src="c.png">`
	}
	uri := func(mime, data string) string {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString([]byte(data))
	}

	got, missing, err := inlineLocalImages(page(chart, icon, photo))
	if err != nil || missing != nil {
		t.Fatalf("err=%v missing=%q", err, missing)
	}
	if want := page(uri("image/png", pngSignature), uri("image/svg+xml", "<svg/>"), uri("image/jpeg", jpegHeader)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestInlineLocalImagesLeavesAPageWithoutImagesAlone(t *testing.T) {
	html := `<p>No pictures.</p>`
	if got, missing, err := inlineLocalImages(html); err != nil || missing != nil || got != html {
		t.Fatalf("got %q missing=%q err=%v", got, missing, err)
	}
}

func TestInlineLocalImagesOpensARepeatedImageOnce(t *testing.T) {
	chart := writeImage(t, t.TempDir(), "chart.png", pngSignature)
	opens := 0
	wrapOpenRenderImage(t, func(name string, open func(string) (*os.File, error)) (*os.File, error) {
		opens++
		return open(name)
	})

	got, _, err := inlineLocalImages(strings.Repeat(`<img src="`+chart+`">`, 3))
	if err != nil || opens != 1 {
		t.Fatalf("err=%v opens=%d", err, opens)
	}
	if want := strings.Repeat(`<img src="data:image/png;base64,iVBORw0KGgo=">`, 3); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestInlineLocalImagesReportsWhatItCannotInline(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent.gif")
	folder := filepath.Join(dir, "album.jpg")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	env := writeImage(t, dir, "env.png", "DATABASE_URL=postgres://u:p@db/app")
	page := writeImage(t, dir, "page.svg", "<html><body><svg/></body></html>")
	html := `<img src="` + absent + `"><img src="` + folder + `"><img src="` + env + `">` +
		`<img src="` + page + `"><script>load("Q:\\none\\x.webp")</script><img src="` + absent + `">`

	got, missing, err := inlineLocalImages(html)
	if err != nil || got != html {
		t.Fatalf("err=%v page=%s", err, got)
	}
	if want := []string{absent, folder, env, page, `Q:\\none\\x.webp`}; !slices.Equal(missing, want) {
		t.Fatalf("missing %q, want %q", missing, want)
	}
}

func TestInlineLocalImagesReportsASymlinkToANonImage(t *testing.T) {
	dir := t.TempDir()
	key := writeImage(t, dir, "id_ed25519", "-----BEGIN OPENSSH "+"PRIVATE KEY-----")
	link := filepath.Join(dir, "avatar.png")
	if err := os.Symlink(key, link); err != nil {
		t.Skipf("cannot symlink: %v", err)
	}
	html := `<img src="` + link + `">`
	got, missing, err := inlineLocalImages(html)
	if err != nil || got != html || !slices.Equal(missing, []string{link}) {
		t.Fatalf("err=%v missing=%q page=%s", err, missing, got)
	}
}

func TestInlineLocalImagesSizeLimits(t *testing.T) {
	dir := t.TempDir()
	huge := bigPNG(t, dir, "huge.png", 12<<20)
	nine := bigPNG(t, dir, "nine.png", 9<<20)
	six := bigPNG(t, dir, "six.png", 6<<20)
	for _, tc := range []struct {
		name string
		html string
		want string
	}{
		{"one image over the limit", `<img src="` + six + `"><img src="` + huge + `">`,
			"local image " + huge + " exceeds 10.0 MiB (12.0 MiB); resize it before rendering"},
		{"a page that grows past the limit", `<img src="` + nine + `"><img src="` + six + `">` + `<img src="` + nine + `">`,
			"the page exceeds the 25.0 MiB limit after embedding images (32.0 MiB); use smaller images"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := inlineLocalImages(tc.html); err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v\nwant  %s", err, tc.want)
			}
		})
	}
}

func TestInlineLocalImagesCatchesAnImageThatGrowsAfterItsStat(t *testing.T) {
	chart := writeImage(t, t.TempDir(), "chart.png", pngSignature)
	wrapOpenRenderImage(t, func(name string, open func(string) (*os.File, error)) (*os.File, error) {
		if err := os.Truncate(name, 64<<20); err != nil {
			t.Fatal(err)
		}
		return open(name)
	})

	_, _, err := inlineLocalImages(`<img src="` + chart + `">`)
	if want := "local image " + chart + " exceeds 10.0 MiB (over 10.0 MiB); resize it before rendering"; err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
}

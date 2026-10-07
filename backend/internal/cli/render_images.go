package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// A render page runs in an opaque-origin sandbox, so it cannot load a local
// file. The CLI runs as the agent, with the agent's file access, and inlines
// every local image the page names by absolute path. Ported from T3 Code's
// HtmlRender.inlineLocalImages.

const (
	maxRenderImageBytes = 10 << 20
	// maxRenderPageBytes matches the daemon's limit on a render page.
	maxRenderPageBytes = 25 << 20
	// maxImagePathChars bounds a reference the way T3's {0,2048} does; RE2
	// cannot count that high, so findLocalImages checks it.
	maxImagePathChars = 2048
)

var imageMIMETypes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
	"avif": "image/avif",
	"svg":  "image/svg+xml",
	"bmp":  "image/bmp",
	"ico":  "image/x-icon",
}

// localImagePattern matches an absolute image path that is a whole quoted
// string ("…", '…', `…`) on one line, or an unquoted CSS url(…). An absolute
// path is POSIX /… (not protocol-relative //…) or Windows C:\… or C:/…. URLs,
// data:, blob:, and relative paths never match. RE2 has neither
// backreferences nor lookahead, so each quote gets its own alternative, and
// "/ not followed by /" is spelled out.
var localImagePattern = func() *regexp.Regexp {
	ext := `\.(?:` + strings.Join(slices.Sorted(maps.Keys(imageMIMETypes)), "|") + `)`
	path := func(stop string) string {
		return `((?:/(?:[^/` + stop + `][^` + stop + `]*?)?|[a-z]:[\\/][^` + stop + `]*?)` + ext + `)`
	}
	quoted := func(quote string) string { return quote + path(quote+`\r\n`) + quote }
	return regexp.MustCompile(`(?i)` + quoted(`"`) + `|` + quoted(`'`) + `|` + quoted(`\x60`) +
		`|url\(\s*` + path(`\s"'\x60()`) + `\s*\)`)
}()

var windowsDrive = regexp.MustCompile(`(?i)^[a-z]:`)

// openRenderImage opens without blocking, so a FIFO swapped in after the stat
// cannot hang the read; readRenderImage then refuses it. Tests wrap it.
var openRenderImage = func(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

type localImage struct {
	start, end int
	path       string
}

func findLocalImages(html string) []localImage {
	var refs []localImage
	for at := 0; at < len(html); {
		match := localImagePattern.FindStringSubmatchIndex(html[at:])
		if match == nil {
			break
		}
		group := 2
		for match[group] < 0 {
			group += 2
		}
		start, end := at+match[group], at+match[group+1]
		if utf8.RuneCountInString(html[start:end]) > maxImagePathChars {
			// T3's bounded pattern fails here and tries the next position.
			at += match[0] + 1
			continue
		}
		refs = append(refs, localImage{start: start, end: end, path: html[start:end]})
		at += match[1]
	}
	return refs
}

// imageFilePath unescapes a Windows path written inside a JS string literal.
func imageFilePath(ref string) string {
	if windowsDrive.MatchString(ref) {
		return strings.ReplaceAll(ref, `\\`, `\`)
	}
	return ref
}

func formatMiB(n int) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// renderImageError is a whole sentence the agent reads as is.
type renderImageError string

func (e renderImageError) Error() string { return string(e) }

// missingImagesError refuses a page that names images nobody can read.
func missingImagesError(missing []string) error {
	return renderImageError("These local images could not be read: " + strings.Join(missing, ", ") +
		". Use absolute paths to existing image files, or remove them.")
}

// imageTooLarge names an image's size, "12.0 MiB" or "over 10.0 MiB".
func imageTooLarge(path, size string) error {
	return renderImageError(fmt.Sprintf("%s is %s; each local image must be at most %s.",
		path, size, formatMiB(maxRenderImageBytes)))
}

func pageTooLarge(size int) error {
	return renderImageError(fmt.Sprintf("With its images inlined the page is %s; the limit is %s. Use smaller images.",
		formatMiB(size), formatMiB(maxRenderPageBytes)))
}

func dataURIPrefix(path string) string {
	mime, ok := imageMIMETypes[strings.ToLower(path[strings.LastIndexByte(path, '.')+1:])]
	if !ok {
		mime = "application/octet-stream"
	}
	return "data:" + mime + ";base64,"
}

// inlineLocalImages replaces every absolute local image reference with a data URI.
// missing lists references that could not be read or are not images; they stay as written.
func inlineLocalImages(html string) (inlined string, missing []string, err error) {
	refs := findLocalImages(html)
	var paths []string
	sizes := map[string]int{}
	for _, ref := range refs {
		if _, seen := sizes[ref.path]; seen {
			continue
		}
		paths = append(paths, ref.path)
		sizes[ref.path] = -1
		if info, err := os.Stat(imageFilePath(ref.path)); err == nil && info.Mode().IsRegular() {
			sizes[ref.path] = int(info.Size())
		}
	}
	for _, path := range paths {
		if sizes[path] > maxRenderImageBytes {
			return "", nil, imageTooLarge(path, formatMiB(sizes[path]))
		}
	}
	pageBytes := len(html)
	for _, ref := range refs {
		if size := sizes[ref.path]; size >= 0 {
			pageBytes += len(dataURIPrefix(ref.path)) + base64.StdEncoding.EncodedLen(size) - len(ref.path)
		}
	}
	if pageBytes > maxRenderPageBytes {
		return "", nil, pageTooLarge(pageBytes)
	}
	// Files can grow after the stat. Each read stops one byte past the image
	// limit, and reading stops once the images read so far cannot fit the page.
	dataURIs := map[string]string{}
	readBytes := 0
	for _, path := range paths {
		if sizes[path] < 0 {
			continue
		}
		data, ok := readRenderImage(imageFilePath(path))
		if !ok || !isImageBytes(data) {
			continue
		}
		if len(data) > maxRenderImageBytes {
			// The read stopped one byte past the limit, so the real size is unknown.
			return "", nil, imageTooLarge(path, "over "+formatMiB(maxRenderImageBytes))
		}
		readBytes += base64.StdEncoding.EncodedLen(len(data))
		if readBytes > maxRenderPageBytes {
			return "", nil, pageTooLarge(readBytes)
		}
		dataURIs[path] = dataURIPrefix(path) + base64.StdEncoding.EncodeToString(data)
	}
	var b strings.Builder
	cursor := 0
	for _, ref := range refs {
		if uri, ok := dataURIs[ref.path]; ok {
			b.WriteString(html[cursor:ref.start])
			b.WriteString(uri)
			cursor = ref.end
		}
	}
	b.WriteString(html[cursor:])
	if b.Len() > maxRenderPageBytes {
		return "", nil, pageTooLarge(b.Len())
	}
	for _, path := range paths {
		if _, ok := dataURIs[path]; !ok {
			missing = append(missing, path)
		}
	}
	return b.String(), missing, nil
}

func readRenderImage(path string) ([]byte, bool) {
	file, err := openRenderImage(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRenderImageBytes+1))
	return data, err == nil
}

// isImageBytes reports whether file bytes are an image, whatever the file is
// named, so a symlink or renamed file cannot carry other data, such as a
// secret, into a page.
func isImageBytes(data []byte) bool {
	head := string(data[:min(len(data), 12)])
	switch {
	case strings.HasPrefix(head, "\x89PNG"),
		strings.HasPrefix(head, "\xff\xd8\xff"),
		strings.HasPrefix(head, "GIF8"),
		strings.HasPrefix(head, "\x00\x00\x01\x00"),
		strings.HasPrefix(head, "BM") && len(head) >= 10 && head[6:10] == "\x00\x00\x00\x00",
		strings.HasPrefix(head, "RIFF") && len(head) == 12 && head[8:12] == "WEBP",
		len(head) == 12 && (head[4:] == "ftypavif" || head[4:] == "ftypavis" || head[4:] == "ftypmif1"):
		return true
	}
	return hasSVGRoot(string(data[:min(len(data), 4096)]))
}

// isJSSpace matches JavaScript's \s, which T3's hasSvgRoot skips.
func isJSSpace(r rune) bool {
	return r == '\uFEFF' || (r != '\u0085' && unicode.IsSpace(r))
}

// indexAfter returns the index just past token at or after from, or -1 when it never appears.
func indexAfter(text, token string, from int) int {
	at := strings.Index(text[from:], token)
	if at < 0 {
		return -1
	}
	return from + at + len(token)
}

// hasSVGRoot reports whether an XML document's root element is <svg>, after
// any processing instructions, comments, and a doctype. One forward pass, so
// no input can make it slow, and quoted text never counts as markup.
func hasSVGRoot(text string) bool {
	at := 0
	for at != -1 {
		for at < len(text) {
			r, size := utf8.DecodeRuneInString(text[at:])
			if !isJSSpace(r) {
				break
			}
			at += size
		}
		switch rest := text[at:]; {
		case strings.HasPrefix(rest, "<?"):
			at = indexAfter(text, "?>", at+2)
		case strings.HasPrefix(rest, "<!--"):
			at = indexAfter(text, "-->", at+4)
		case len(rest) >= 9 && strings.EqualFold(rest[:9], "<!doctype"):
			at = afterDoctype(text, at+9)
		default:
			// XML names are case-sensitive, and only these characters can end one here.
			return len(rest) >= 5 && strings.HasPrefix(rest, "<svg") && strings.ContainsRune(" \t\r\n/>", rune(rest[4]))
		}
	}
	return false
}

// afterDoctype returns the index just past a doctype whose body starts at
// from, honoring quotes and its internal subset, or -1 when it never ends.
func afterDoctype(text string, from int) int {
	inSubset := false
	at := from
	for at != -1 && at < len(text) {
		switch c := text[at]; {
		case c == '"' || c == '\'':
			at = indexAfter(text, string(c), at+1)
		case inSubset && strings.HasPrefix(text[at:], "<!--"):
			at = indexAfter(text, "-->", at+4)
		case inSubset && strings.HasPrefix(text[at:], "<?"):
			at = indexAfter(text, "?>", at+2)
		case c == '>' && !inSubset:
			return at + 1
		case c == '[':
			inSubset = true
			at++
		case c == ']':
			inSubset = false
			at++
		default:
			at++
		}
	}
	return -1
}

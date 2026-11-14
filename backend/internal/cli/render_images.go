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

const (
	maxRenderImageBytes = 10 << 20
	maxRenderPageBytes  = 25 << 20
	maxImagePathChars   = 2048
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
			at += match[0] + 1
			continue
		}
		refs = append(refs, localImage{start: start, end: end, path: html[start:end]})
		at += match[1]
	}
	return refs
}

func imageFilePath(ref string) string {
	if windowsDrive.MatchString(ref) {
		return strings.ReplaceAll(ref, `\\`, `\`)
	}
	return ref
}

func formatMiB(n int) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

type renderImageError string

func (e renderImageError) Error() string { return string(e) }

func missingImagesError(missing []string) error {
	return renderImageError("These local images could not be read: " + strings.Join(missing, ", ") +
		". Use absolute paths to existing image files, or remove them.")
}

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

func isJSSpace(r rune) bool {
	return r == '\uFEFF' || (r != '\u0085' && unicode.IsSpace(r))
}

func indexAfter(text, token string, from int) int {
	at := strings.Index(text[from:], token)
	if at < 0 {
		return -1
	}
	return from + at + len(token)
}

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
			return len(rest) >= 5 && strings.HasPrefix(rest, "<svg") && strings.ContainsRune(" \t\r\n/>", rune(rest[4]))
		}
	}
	return false
}

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

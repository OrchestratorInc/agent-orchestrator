package mcpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

// A render page runs in an opaque-origin sandbox and cannot load a local file.
// The CLI runs with the agent's file access, so it swaps each local image the
// page names by absolute path for a data URI before publishing.

const (
	maxRenderImageBytes = 10 << 20
	// maxRenderPageBytes matches the daemon's limit on a render page.
	maxRenderPageBytes = 25 << 20
	maxImagePathChars  = 2048
)

var imageExtensions = map[string]bool{
	".avif": true, ".bmp": true, ".gif": true, ".ico": true, ".jpeg": true,
	".jpg": true, ".png": true, ".svg": true, ".webp": true,
}

// openRenderImage opens without blocking, so a FIFO swapped in after the stat
// cannot hang the read; readRenderImage then refuses it. Tests wrap it.
var openRenderImage = func(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

type localImage struct {
	start, end int
	path       string
}

// findLocalImages finds the absolute image paths a page names, each either a
// whole one-line string in "…", '…' or `…`, or the unquoted argument of a CSS
// url(…). URLs, data: and blob: URIs, and relative paths are left alone.
func findLocalImages(html string) []localImage {
	var refs []localImage
	for at := 0; at < len(html); at++ {
		skip := strings.IndexAny(html[at:], "\"'`uU")
		if skip < 0 {
			break
		}
		at += skip
		start, end, ok := referenceAt(html, at)
		if ok && isLocalImagePath(html[start:end]) {
			refs = append(refs, localImage{start: start, end: end, path: html[start:end]})
			at = end
		}
	}
	return refs
}

// referenceAt returns the span of the string quoted at html[at], or of the
// unquoted url( argument starting there.
func referenceAt(html string, at int) (start, end int, ok bool) {
	if quote := html[at]; quote == '"' || quote == '\'' || quote == '`' {
		length := strings.IndexAny(html[at+1:], string(quote)+"\r\n")
		if length < 0 || html[at+1+length] != quote {
			return 0, 0, false
		}
		return at + 1, at + 1 + length, true
	}
	if !strings.HasPrefix(strings.ToLower(html[at:min(len(html), at+4)]), "url(") {
		return 0, 0, false
	}
	start = skipCSSSpace(html, at+4)
	end = start
	for end < len(html) && !strings.ContainsRune(" \t\n\f\r\"'`()", rune(html[end])) {
		end++
	}
	closing := skipCSSSpace(html, end)
	return start, end, closing < len(html) && html[closing] == ')'
}

func skipCSSSpace(html string, at int) int {
	for at < len(html) && strings.ContainsRune(" \t\n\f\r", rune(html[at])) {
		at++
	}
	return at
}

// isLocalImagePath reports whether ref is an absolute POSIX path (/…, not the
// protocol-relative //…) or Windows path (C:\… or C:/…) with an image extension.
func isLocalImagePath(ref string) bool {
	posix := strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "//")
	if !posix && !hasDriveLetter(ref) {
		return false
	}
	dot := strings.LastIndexByte(ref, '.')
	return dot >= 0 && imageExtensions[strings.ToLower(ref[dot:])] && utf8.RuneCountInString(ref) <= maxImagePathChars
}

func hasDriveLetter(ref string) bool {
	if len(ref) < 3 || ref[1] != ':' || (ref[2] != '\\' && ref[2] != '/') {
		return false
	}
	letter := ref[0] | 0x20
	return letter >= 'a' && letter <= 'z'
}

// imageFilePath unescapes a Windows path written inside a JS string literal.
func imageFilePath(ref string) string {
	if hasDriveLetter(ref) {
		return strings.ReplaceAll(ref, `\\`, `\`)
	}
	return ref
}

func formatMiB(n int) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// MissingImagesError refuses a page that names images nobody can read.
func MissingImagesError(missing []string) error {
	return errors.New("Cannot inline these local images: " + strings.Join(missing, ", ") +
		". Check their paths, permissions, and image formats.")
}

// imageTooLarge names an image's size, "12.0 MiB" or "over 10.0 MiB".
func imageTooLarge(path, size string) error {
	return fmt.Errorf("local image %s exceeds %s (%s); resize it before rendering", path, formatMiB(maxRenderImageBytes), size)
}

func pageTooLarge(size int) error {
	return fmt.Errorf("the page exceeds the %s limit after embedding images (%s); use smaller images",
		formatMiB(maxRenderPageBytes), formatMiB(size))
}

// InlineLocalImages replaces every absolute local image reference with a data URI.
// missing lists references that could not be read or are not images; they stay as written.
func InlineLocalImages(html string) (inlined string, missing []string, err error) {
	refs := findLocalImages(html)

	// Stat before opening anything: opening a FIFO blocks until a writer comes.
	var paths []string
	sizes := map[string]int{}
	for _, ref := range refs {
		if _, seen := sizes[ref.path]; seen {
			continue
		}
		paths = append(paths, ref.path)
		sizes[ref.path] = -1
		info, err := os.Stat(imageFilePath(ref.path))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxRenderImageBytes {
			return "", nil, imageTooLarge(ref.path, formatMiB(int(info.Size())))
		}
		sizes[ref.path] = int(info.Size())
	}
	projected := len(html)
	for _, ref := range refs {
		if size := sizes[ref.path]; size >= 0 {
			projected += base64.StdEncoding.EncodedLen(size) - len(ref.path)
		}
	}
	if projected > maxRenderPageBytes {
		return "", nil, pageTooLarge(projected)
	}

	// A file can grow after its stat, so every read is capped and counted again.
	uris := map[string]string{}
	encoded := 0
	for _, path := range paths {
		if sizes[path] < 0 {
			continue
		}
		data, ok := readRenderImage(imageFilePath(path))
		if !ok {
			continue
		}
		if len(data) > maxRenderImageBytes {
			return "", nil, imageTooLarge(path, "over "+formatMiB(maxRenderImageBytes))
		}
		mime, ok := imageType(data)
		if !ok {
			continue
		}
		encoded += base64.StdEncoding.EncodedLen(len(data))
		if encoded > maxRenderPageBytes {
			return "", nil, pageTooLarge(encoded)
		}
		uris[path] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}

	var page strings.Builder
	last := 0
	for _, ref := range refs {
		if uri, ok := uris[ref.path]; ok {
			page.WriteString(html[last:ref.start])
			page.WriteString(uri)
			last = ref.end
		}
	}
	page.WriteString(html[last:])
	if page.Len() > maxRenderPageBytes {
		return "", nil, pageTooLarge(page.Len())
	}
	for _, path := range paths {
		if _, ok := uris[path]; !ok {
			missing = append(missing, path)
		}
	}
	return page.String(), missing, nil
}

// readRenderImage reads a regular file, stopping one byte past the image limit.
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

// imageType names the image format of data from its bytes, never from the
// file's name, so a renamed file or a symlink cannot carry other data, such
// as a secret, into a page.
func imageType(data []byte) (string, bool) {
	switch mime := http.DetectContentType(data); {
	case mime == "image/png", mime == "image/jpeg", mime == "image/gif", mime == "image/webp", mime == "image/x-icon":
		return mime, true
	case mime == "image/bmp" && isBMP(data):
		return mime, true
	case isAVIF(data):
		return "image/avif", true
	case isSVG(data):
		return "image/svg+xml", true
	}
	return "", false
}

// isBMP checks the size of the info header that follows the 14-byte file
// header; every BMP version uses one of these, so text that merely starts
// with "BM" is refused.
func isBMP(data []byte) bool {
	if len(data) < 18 {
		return false
	}
	switch binary.LittleEndian.Uint32(data[14:18]) {
	case 12, 16, 40, 52, 56, 64, 108, 124:
		return true
	}
	return false
}

// isAVIF reports whether data opens with an ISO BMFF ftyp box that lists an
// AVIF brand, as its major brand or among its compatible brands.
func isAVIF(data []byte) bool {
	if len(data) < 16 || string(data[4:8]) != "ftyp" {
		return false
	}
	size := int(binary.BigEndian.Uint32(data))
	if size < 16 || size > len(data) || size%4 != 0 {
		return false
	}
	avifBrand := func(at int) bool {
		brand := string(data[at : at+4])
		return brand == "avif" || brand == "avis"
	}
	if avifBrand(8) {
		return true
	}
	for at := 16; at < size; at += 4 {
		if avifBrand(at) {
			return true
		}
	}
	return false
}

// isSVG reports whether data is XML whose root element is <svg>.
func isSVG(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff"))))
	decoder.Strict = false
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if decoded, err := charset.NewReaderLabel(label, input); err == nil {
			return decoded, nil
		}
		return input, nil
	}
	for {
		token, err := decoder.RawToken()
		if err != nil {
			return false
		}
		switch token := token.(type) {
		case xml.StartElement:
			return token.Name.Space == "" && token.Name.Local == "svg"
		case xml.CharData:
			if len(bytes.TrimSpace(token)) > 0 {
				return false
			}
		}
	}
}

package httpd

import (
	"bufio"
	"compress/flate"
	"compress/zlib"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
)

func remoteJSONCompression(next http.Handler) http.Handler {
	compressor := middleware.NewCompressor(flate.BestSpeed, "application/json")
	compressor.SetEncoder("deflate", func(w io.Writer, level int) io.Writer {
		writer, err := zlib.NewWriterLevel(w, level)
		if err != nil {
			return nil
		}
		return writer
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = &remoteJSONResponseWriter{ResponseWriter: w}
		encoding := preferredRemoteEncoding(r.Header.Values("Accept-Encoding"))
		if encoding == "" || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}

		negotiated := r.Clone(r.Context())
		negotiated.Header.Set("Accept-Encoding", encoding)
		compressor.Handler(http.HandlerFunc(func(encoded http.ResponseWriter, _ *http.Request) {
			next.ServeHTTP(&remoteJSONResponseWriter{ResponseWriter: encoded, uncompressed: w}, r)
		})).ServeHTTP(w, negotiated)
	})
}

type remoteJSONResponseWriter struct {
	http.ResponseWriter
	uncompressed http.ResponseWriter
	wroteHeader  bool
}

func (w *remoteJSONResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if w.uncompressed == nil {
			var variesByEncoding bool
			for _, field := range w.Header().Values("Vary") {
				for _, token := range strings.Split(field, ",") {
					token = strings.TrimSpace(token)
					if token == "*" || strings.EqualFold(token, "Accept-Encoding") {
						variesByEncoding = true
					}
				}
			}
			if !variesByEncoding {
				w.Header().Add("Vary", "Accept-Encoding")
			}
		} else if status == http.StatusPartialContent {
			w.ResponseWriter = w.uncompressed
		} else {
			contentType, _, _ := strings.Cut(w.Header().Get("Content-Type"), ";")
			if (contentType == "application/json" || (status == http.StatusNotModified && contentType == "")) && w.Header().Get("Content-Encoding") == "" {
				if etag := w.Header().Get("ETag"); strings.HasPrefix(etag, `"`) {
					w.Header().Set("ETag", "W/"+etag)
				}
			}
			if status == http.StatusNotModified {
				w.ResponseWriter = w.uncompressed
			}
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *remoteJSONResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *remoteJSONResponseWriter) FlushError() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *remoteJSONResponseWriter) Flush() { _ = w.FlushError() }

func (w *remoteJSONResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *remoteJSONResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (w *remoteJSONResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func preferredRemoteEncoding(fields []string) string {
	qualities := make(map[string]int, 4)
	for _, field := range fields {
		for _, entry := range strings.Split(field, ",") {
			coding, weight, weighted := strings.Cut(entry, ";")
			coding = strings.ToLower(strings.TrimSpace(coding))
			switch coding {
			case "gzip", "deflate", "identity", "*":
			default:
				continue
			}
			quality := 1000
			if weighted {
				key, value, ok := strings.Cut(strings.TrimSpace(weight), "=")
				quality = 0
				if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
					quality = remoteEncodingQuality(strings.TrimSpace(value))
				}
			}
			if previous, ok := qualities[coding]; !ok || quality < previous {
				qualities[coding] = quality
			}
		}
	}

	var selected string
	best := 0
	for _, coding := range []string{"gzip", "deflate"} {
		quality, explicit := qualities[coding]
		if !explicit {
			quality = qualities["*"]
		}
		if quality > best {
			selected, best = coding, quality
		}
	}
	if qualities["identity"] > best {
		return ""
	}
	return selected
}

func remoteEncodingQuality(value string) int {
	if value == "0" {
		return 0
	}
	if value == "1" {
		return 1000
	}
	whole, fraction, ok := strings.Cut(value, ".")
	if !ok || (whole != "0" && whole != "1") || len(fraction) > 3 {
		return 0
	}
	quality, scale := 0, 100
	for _, digit := range fraction {
		if digit < '0' || digit > '9' {
			return 0
		}
		quality += int(digit-'0') * scale
		scale /= 10
	}
	if whole == "1" {
		if quality != 0 {
			return 0
		}
		return 1000
	}
	return quality
}

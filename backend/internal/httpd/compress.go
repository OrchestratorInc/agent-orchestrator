package httpd

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// jsonGzipMinBytes is the smallest JSON body compressJSON gzips: below about a
// kilobyte the gzip framing and the compressor's time outweigh the saving.
const jsonGzipMinBytes = 1024

// jsonGzipLevel is cheap enough for loopback, where the renderer gains little
// from fewer bytes: a 1 MB conversation page takes about 3 ms at BestSpeed and
// about twice that at the default level, for a gzip only 15% smaller.
const jsonGzipLevel = gzip.BestSpeed

// jsonGzipWriters reuses compressors: each holds hundreds of KB of state.
var jsonGzipWriters = sync.Pool{New: func() any {
	gz, _ := gzip.NewWriterLevel(nil, jsonGzipLevel)
	return gz
}}

// compressJSON gzips JSON responses of at least jsonGzipMinBytes for a client
// that accepts gzip. Only application/json is compressed, so event streams,
// files and other binary bodies pass as written. Upgrades (the terminal mux,
// the tunnel) and preview routes are left alone: a preview carries a dev
// server's own encoding and streams, and compressPreviewResponse already
// handles it.
func compressJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Upgrade") != "" || r.Header.Get("Range") != "" ||
			isPreviewPath(r.URL.Path) || !controllers.AcceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &gzipJSONWriter{ResponseWriter: w}
		next.ServeHTTP(cw, r)
		cw.finish()
	})
}

func isPreviewPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/api/v1/sessions/")
	return ok && (strings.Contains(rest, "/preview/") || strings.Contains(rest, "/agent-browser/"))
}

// gzipJSONWriter holds back a JSON body until it reaches jsonGzipMinBytes, then
// sends it gzipped. A shorter body, or one flushed before then, goes out as
// written.
type gzipJSONWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	pending     bool // a JSON body is being held back
	buf         []byte
	gz          *gzip.Writer
}

func (cw *gzipJSONWriter) WriteHeader(status int) {
	if cw.wroteHeader {
		cw.ResponseWriter.WriteHeader(status)
		return
	}
	cw.wroteHeader = true
	h := cw.Header()
	if strings.HasPrefix(h.Get("Content-Type"), "application/json") && h.Get("Content-Encoding") == "" {
		cw.status = status
		cw.pending = true
		return
	}
	cw.ResponseWriter.WriteHeader(status)
}

func (cw *gzipJSONWriter) Write(p []byte) (int, error) {
	if !cw.wroteHeader {
		cw.WriteHeader(http.StatusOK)
	}
	switch {
	case cw.gz != nil:
		return cw.gz.Write(p)
	case !cw.pending:
		return cw.ResponseWriter.Write(p)
	}
	cw.buf = append(cw.buf, p...)
	if len(cw.buf) >= jsonGzipMinBytes {
		if err := cw.startGzip(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (cw *gzipJSONWriter) startGzip() error {
	h := cw.Header()
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	h.Del("Content-Length")
	cw.pending = false
	cw.ResponseWriter.WriteHeader(cw.status)
	cw.gz, _ = jsonGzipWriters.Get().(*gzip.Writer) // New makes nothing else
	cw.gz.Reset(cw.ResponseWriter)
	_, err := cw.gz.Write(cw.buf)
	cw.buf = nil
	return err
}

// sendPending sends a held-back body as written.
func (cw *gzipJSONWriter) sendPending() {
	cw.pending = false
	cw.ResponseWriter.WriteHeader(cw.status)
	if len(cw.buf) > 0 {
		_, _ = cw.ResponseWriter.Write(cw.buf)
	}
	cw.buf = nil
}

func (cw *gzipJSONWriter) Flush() {
	if !cw.wroteHeader {
		cw.WriteHeader(http.StatusOK)
	}
	if cw.pending {
		cw.sendPending()
	}
	if cw.gz != nil {
		_ = cw.gz.Flush()
	}
	_ = http.NewResponseController(cw.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the connection's own writer.
func (cw *gzipJSONWriter) Unwrap() http.ResponseWriter { return cw.ResponseWriter }

func (cw *gzipJSONWriter) finish() {
	if cw.pending {
		cw.sendPending()
	}
	if cw.gz != nil {
		_ = cw.gz.Close()
		cw.gz.Reset(nil) // the pool keeps the compressor, not the response
		jsonGzipWriters.Put(cw.gz)
		cw.gz = nil
	}
}

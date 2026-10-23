// Package s3fake is an in-process S3 server for tests. It implements the
// versioned PutObject, GetObject, HeadObject and DeleteObject calls that
// artifactstore makes, including x-amz-checksum-sha256 verification.
package s3fake

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
)

// Bucket is the bucket name the fake serves.
const Bucket = "ao-test-tenant-artifacts"

type version struct {
	id   string
	body []byte
	sum  []byte
}

// Server is a running fake S3 endpoint.
type Server struct {
	server *httptest.Server

	mu          sync.Mutex
	objects     map[string][]version
	markers     map[string]int
	puts        int
	gets        int
	failPuts    bool
	failDeletes bool
}

// New starts a fake S3 server and stops it when the test ends. It also points
// the default AWS credential chain at static test credentials.
func New(t testing.TB) *Server {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.DevNull)
	fake := &Server{objects: map[string][]version{}, markers: map[string]int{}}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

// Config is the artifactstore configuration for this fake.
func (f *Server) Config() artifactstore.Config {
	return artifactstore.Config{Bucket: Bucket, Region: "us-east-1", Endpoint: f.server.URL}
}

// Versions returns how many versions key has.
func (f *Server) Versions(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects[key])
}

// DeleteMarkers returns how many delete markers key has.
func (f *Server) DeleteMarkers(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.markers[key]
}

// Puts returns how many PutObject calls succeeded.
func (f *Server) Puts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

// Gets returns how many GetObject calls it received.
func (f *Server) Gets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// Remove deletes every version of key.
func (f *Server) Remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
}

// FailDeletes makes every DeleteObject return 500 while fail is true.
func (f *Server) FailDeletes(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failDeletes = fail
}

// FailPuts makes every PutObject return 500 while fail is true.
func (f *Server) FailPuts(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPuts = fail
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if bucket != Bucket || key == "" {
		writeError(w, r, http.StatusNotFound, "NoSuchBucket")
		return
	}
	versionID := r.URL.Query().Get("versionId")
	switch r.Method {
	case http.MethodPut:
		f.put(w, r, key)
	case http.MethodGet, http.MethodHead:
		if r.Method == http.MethodGet {
			f.mu.Lock()
			f.gets++
			f.mu.Unlock()
		}
		f.get(w, r, key, versionID)
	case http.MethodDelete:
		f.delete(w, key, versionID)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func (f *Server) put(w http.ResponseWriter, r *http.Request, key string) {
	if r.ContentLength < 0 {
		writeError(w, r, http.StatusLengthRequired, "MissingContentLength")
		return
	}
	if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
		writeError(w, r, http.StatusBadRequest, "UnsupportedEncoding")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil || int64(len(body)) != r.ContentLength {
		writeError(w, r, http.StatusBadRequest, "IncompleteBody")
		return
	}
	sum := sha256.Sum256(body)
	claimed, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Amz-Checksum-Sha256"))
	if err != nil || !bytes.Equal(claimed, sum[:]) {
		writeError(w, r, http.StatusBadRequest, "XAmzContentChecksumMismatch")
		return
	}
	f.mu.Lock()
	if f.failPuts {
		f.mu.Unlock()
		writeError(w, r, http.StatusInternalServerError, "InternalError")
		return
	}
	stored := version{id: uuid.NewString(), body: body, sum: sum[:]}
	f.objects[key] = append(f.objects[key], stored)
	f.puts++
	f.mu.Unlock()
	w.Header().Set("x-amz-version-id", stored.id)
	w.Header().Set("x-amz-checksum-sha256", base64.StdEncoding.EncodeToString(stored.sum))
	w.Header().Set("ETag", `"`+stored.id+`"`)
	w.WriteHeader(http.StatusOK)
}

func (f *Server) get(w http.ResponseWriter, r *http.Request, key, versionID string) {
	f.mu.Lock()
	versions := f.objects[key]
	var found *version
	for index := range versions {
		if versionID == "" || versions[index].id == versionID {
			found = &versions[index]
		}
	}
	f.mu.Unlock()
	if found == nil {
		code := "NoSuchKey"
		if len(versions) > 0 {
			code = "NoSuchVersion"
		}
		writeError(w, r, http.StatusNotFound, code)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(found.body)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("x-amz-version-id", found.id)
	w.Header().Set("ETag", `"`+found.id+`"`)
	if strings.EqualFold(r.Header.Get("X-Amz-Checksum-Mode"), "ENABLED") {
		w.Header().Set("x-amz-checksum-sha256", base64.StdEncoding.EncodeToString(found.sum))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(found.body)
	}
}

func (f *Server) delete(w http.ResponseWriter, key, versionID string) {
	f.mu.Lock()
	if f.failDeletes {
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code></Error>`))
		return
	}
	if versionID == "" {
		f.markers[key]++
		f.mu.Unlock()
		w.Header().Set("x-amz-delete-marker", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	versions := f.objects[key]
	kept := versions[:0]
	for _, stored := range versions {
		if stored.id != versionID {
			kept = append(kept, stored)
		}
	}
	f.objects[key] = kept
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}{Code: code, Message: code})
}

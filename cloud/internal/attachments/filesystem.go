package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const StorageRoute = "/api/cloud/v1/attachment-storage/"

type Filesystem struct {
	root   *os.Root
	secret []byte
	writes sync.Mutex
}
type fileGrant struct {
	Key      string
	Metadata Metadata
	Expires  int64
	Read     bool
}

func NewFilesystem(environment, dir string, secret []byte) (*Filesystem, error) {
	if environment != "development" && environment != "test" && environment != "local" {
		return nil, errors.New("filesystem attachments are development-only")
	}
	if len(secret) < 32 {
		return nil, errors.New("attachment signing key must have at least 32 bytes")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Filesystem{root: root, secret: secret}, nil
}
func (f *Filesystem) Close() error { return f.root.Close() }
func (f *Filesystem) token(g fileGrant) (string, error) {
	b, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, f.secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (f *Filesystem) Upload(_ context.Context, key string, m Metadata) (UploadGrant, error) {
	expires := time.Now().Add(UploadTTL)
	token, err := f.token(fileGrant{Key: key, Metadata: m, Expires: expires.Unix()})
	return UploadGrant{URL: StorageRoute + token, Fields: map[string]string{}, ExpiresAt: expires}, err
}
func (f *Filesystem) Read(_ context.Context, key string) (ReadGrant, error) {
	expires := time.Now().Add(ReadTTL)
	token, err := f.token(fileGrant{Key: key, Read: true, Expires: expires.Unix()})
	return ReadGrant{URL: StorageRoute + token, ExpiresAt: expires}, err
}
func (f *Filesystem) Open(_ context.Context, key string) (io.ReadCloser, error) {
	return f.root.Open(key)
}
func (f *Filesystem) Delete(_ context.Context, key string) error {
	f.writes.Lock()
	defer f.writes.Unlock()
	err := f.root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (f *Filesystem) PutVerified(_ context.Context, key string, _ Metadata, data []byte) error {
	f.writes.Lock()
	defer f.writes.Unlock()
	return f.write(key, data)
}
func (f *Filesystem) write(key string, data []byte) error {
	// Generated keys have no path components. os.Root also confines symlinks.
	if strings.ContainsAny(key, "/\\") || key == "" {
		return ErrInvalid
	}
	tempKey := key + "." + uuid.NewString() + ".pending"
	temp, err := f.root.OpenFile(tempKey, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.root.Remove(tempKey)
	_, err = temp.Write(data)
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return f.root.Rename(tempKey, key)
}
func (f *Filesystem) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	token := strings.TrimPrefix(r.URL.Path, StorageRoute)
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		http.Error(w, "invalid grant", 403)
		return
	}
	mac := hmac.New(sha256.New, f.secret)
	mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		http.Error(w, "invalid grant", 403)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	var grant fileGrant
	if err != nil || json.Unmarshal(raw, &grant) != nil || time.Now().Unix() >= grant.Expires {
		http.Error(w, "expired grant", 403)
		return
	}
	if grant.Read {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		file, err := f.root.Open(grant.Key)
		if err != nil {
			w.WriteHeader(404)
			return
		}
		defer file.Close()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, file)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, grant.Metadata.Size+65536)
	reader, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(400)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" {
		w.WriteHeader(400)
		return
	}
	data, err := Verify(part, grant.Metadata)
	if err != nil {
		w.WriteHeader(422)
		return
	}
	if _, err := reader.NextPart(); err != io.EOF {
		w.WriteHeader(400)
		return
	}
	// A POST may begin before expiry and finish reading after cleanup. Check
	// again under the deletion lock so expiry, publication and cleanup cannot
	// interleave and recreate an object after its metadata has been removed.
	f.writes.Lock()
	defer f.writes.Unlock()
	if time.Now().Unix() >= grant.Expires {
		http.Error(w, "expired grant", http.StatusForbidden)
		return
	}
	if err := f.write(grant.Key, data); err != nil {
		w.WriteHeader(500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package mobilebridge

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTheHostKeyIsMadeOnceAndKeptPrivate(t *testing.T) {
	statePath := Path(t.TempDir())
	first, err := HostKey(statePath)
	if err != nil {
		t.Fatal(err)
	}
	again, err := HostKey(statePath)
	if err != nil || !first.Equal(again) {
		t.Fatalf("a second read made another key: %v", err)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(statePath), "host.key"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("host.key mode = %v", info.Mode().Perm())
	}
}

// AO Cloud checks the signature over the method, the path, the time and the
// exact body the machine sent.
func TestTheMachineSignsItsTunnelRequestWithItsHostKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mobile"), 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := HostKey(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	public := ed25519.PublicKey(key[ed25519.SeedSize:])
	verified := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		at, encoded, _ := strings.Cut(r.Header.Get("X-AO-Host-Signature"), ".")
		seconds, _ := strconv.ParseInt(at, 10, 64)
		signature, _ := base64.RawURLEncoding.DecodeString(encoded)
		sum := sha256.Sum256(body)
		message := "ao-host-v1\n" + r.Method + "\n" + r.URL.Path + "\n" + at + "\n" + hex.EncodeToString(sum[:]) + "\n"
		verified = time.Since(time.Unix(seconds, 0)).Abs() < time.Minute && ed25519.Verify(public, []byte(message), signature)
		_ = json.NewEncoder(w).Encode(map[string]any{"hostname": "h-test.example.dev", "runToken": "run-token", "since": time.Now()})
	}))
	defer srv.Close()
	src := NewFixedTunnelSource(srv.URL, dir)
	link(t, src, "account-digest")
	if _, err := src.TunnelFor(context.Background(), "host-uuid", true); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("the tunnel request did not carry the host key's signature")
	}
}

// A fixed vector pins the signed message. AO Cloud's verifier accepts
// signatures from this code, so a change here would split the two apart. A
// control-plane URL with a path prefix signs the path from /api/cloud/ on.
func TestTheHostSignatureMatchesAFixedVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	body := `{"repair":false}`
	req := httptest.NewRequest(http.MethodPost, "https://cp.example/prefix/api/cloud/v1/remote-hosts/h_00000000-0000-0000-0000-000000000001/tunnel", strings.NewReader(body))
	signHostRequest(req, []byte(body), ed25519.NewKeyFromSeed(seed), time.Unix(1767225600, 0))
	const want = "1767225600.ROY8jqC5mA4usb8R60jR4BCUD8VG4MuikbNr1grNqSMdayE6yxc-zjLNYkRL_RxrWZxlDSGCRD3rETg3e77lBQ"
	if got := req.Header.Get("X-AO-Host-Signature"); got != want {
		t.Fatalf("signature = %s\nwant        %s", got, want)
	}
}

// A machine whose clock is off still signs within AO Cloud's 5 minutes: the
// signature carries AO Cloud's time, from the Date of its last answer.
func TestASignatureCarriesAOCloudsTime(t *testing.T) {
	t.Cleanup(func() { aoCloudSkew.Store(0) })
	NoteAOCloudDate(time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	req := httptest.NewRequest(http.MethodPost, "https://cp.example/api/cloud/v1/remote-hosts/h_x/tunnel", nil)
	AuthorizeHostRequest(req, nil, "digest", Path(t.TempDir()))
	at, _, _ := strings.Cut(req.Header.Get("X-AO-Host-Signature"), ".")
	seconds, _ := strconv.ParseInt(at, 10, 64)
	if ahead := time.Until(time.Unix(seconds, 0)); ahead < 59*time.Minute || ahead > 61*time.Minute {
		t.Fatalf("signed %v ahead of this clock, want AO Cloud's hour", ahead)
	}
}

// Without a readable host key the request goes unsigned, which a listing
// without a key and an AO Cloud that takes none still accept.
func TestAMachineWithoutAHostKeySendsTheDigestAlone(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://cp.example/api/cloud/v1/remote-hosts/h_x/tunnel", nil)
	AuthorizeHostRequest(req, nil, "digest", "")
	if req.Header.Get("Authorization") != "Bearer digest" || req.Header.Get("X-AO-Host-Signature") != "" {
		t.Fatalf("headers = %v", req.Header)
	}
}

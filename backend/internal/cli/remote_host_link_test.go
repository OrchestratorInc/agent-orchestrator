package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	linkTestToken   = "abababababababababababababababababababababababababababababababab"
	linkTestHostKey = "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"
	linkFixedHost   = "h-abc.useao.dev"
	linkQuickHost   = "quick.trycloudflare.com"
)

// linkFixture fakes the three parties of `ao remote-host link`: the local
// daemon, WorkOS's device sign-in, and AO Cloud's account machine list.
type linkFixture struct {
	mu sync.Mutex
	// Daemon.
	enabled, loopbackOnly, tunnel, fixed bool
	// AO Cloud.
	listed             string // JSON for the account's hosts
	fixedAddresses     bool
	conflictWithoutURL bool
	connectorStuck     bool // never gets its fixed address
	registrations      []map[string]string
	// WorkOS answers to each poll; the last one repeats.
	polls []string
}

func (f *linkFixture) run(t *testing.T, deps Deps, args ...string) (string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	if f.listed == "" {
		f.listed = "[]"
	}
	if len(f.polls) == 0 {
		f.polls = []string{"ok"}
	}
	cloud := httptest.NewServer(http.HandlerFunc(f.cloud))
	t.Cleanup(cloud.Close)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.daemon(w, r, cloud.URL) }))
	t.Cleanup(daemon.Close)
	writeRunFileFor(t, cfg, daemon)
	workos := httptest.NewServer(http.HandlerFunc(f.workos))
	t.Cleanup(workos.Close)
	previous := workOSAPIBase
	workOSAPIBase = workos.URL
	t.Cleanup(func() { workOSAPIBase = previous })

	if deps.Now == nil {
		deps = (&fakeClock{}).deps()
	}
	deps.ProcessAlive = func(int) bool { return true }
	out, _, err := executeCLI(t, deps, append([]string{"remote-host", "link", "--no-qr"}, args...)...)
	if strings.Contains(out, "pairing-secret") || strings.Contains(out, linkTestToken) {
		t.Fatalf("output leaks a credential: %q", out)
	}
	return out, err
}

func (f *linkFixture) daemon(w http.ResponseWriter, r *http.Request, cloudURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/settings":
		_ = json.NewEncoder(w).Encode(map[string]string{"cloudControlPlaneUrl": cloudURL})
		return
	case "GET /api/v1/mobile/status":
	case "POST /api/v1/mobile/enable-tunnel-only":
		f.enabled, f.loopbackOnly, f.tunnel = true, true, true
	case "POST /api/v1/mobile/remote-access":
		f.tunnel = f.tunnel || f.enabled
	case "POST /api/v1/remote-host/account-token":
		if r.Header.Get("Authorization") != "Bearer pairing-secret" || r.Header.Get("X-AO-Account-ID") != "user_1" {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"hostId": "h_vm", "token": linkTestToken, "hostKey": linkTestHostKey})
		return
	default:
		http.NotFound(w, r)
		return
	}
	endpoints := []map[string]any{}
	if f.tunnel {
		host := linkQuickHost
		if f.fixed {
			host = linkFixedHost
		}
		endpoints = append(endpoints, map[string]any{"kind": "tunnel", "host": host, "port": 443, "secure": true})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"enabled": f.enabled, "loopbackOnly": f.loopbackOnly, "hostId": "h_vm", "password": "pairing-secret",
		"tunnel":    map[string]bool{"supported": true, "running": f.tunnel, "fixed": f.fixed},
		"endpoints": endpoints,
	})
}

func (f *linkFixture) cloud(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer access-1" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/cloud/v1/me/hosts":
		_, _ = io.WriteString(w, `{"hosts":`+f.listed+`,"fixedAddresses":`+map[bool]string{true: "true", false: "false"}[f.fixedAddresses]+`,"hostKeys":true}`)
	case "PUT /api/cloud/v1/me/hosts/h_vm":
		var registration map[string]string
		_ = json.NewDecoder(r.Body).Decode(&registration)
		f.registrations = append(f.registrations, registration)
		if registration["url"] == "" && f.conflictWithoutURL {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"fixed_address_limit_reached"}`)
			return
		}
		// Listed without an address, the machine's connector gets its fixed one.
		f.fixed = registration["url"] == "" && !f.connectorStuck
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *linkFixture) workos(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/user_management/authorize/device":
		_, _ = io.WriteString(w, `{"device_code":"dev-1","user_code":"ABCD-EFGH","verification_uri_complete":"https://auth.example/device?user_code=ABCD-EFGH","expires_in":60,"interval":1}`)
	case "/user_management/authenticate":
		_ = r.ParseForm()
		if r.Form.Get("device_code") != "dev-1" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		answer := f.polls[0]
		if len(f.polls) > 1 {
			f.polls = f.polls[1:]
		}
		if answer != "ok" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"`+answer+`"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"access-1","user":{"id":"user_1","email":"me@example.com"}}`)
	}
}

// fakeClock advances only when the command sleeps, so waits and expiry
// run instantly.
type fakeClock struct{ now time.Time }

func (c *fakeClock) deps() Deps {
	c.now = time.Unix(1_000_000, 0)
	return Deps{Now: func() time.Time { return c.now }, Sleep: func(d time.Duration) { c.now = c.now.Add(d) }}
}

func wantOutput(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(out, part) {
			t.Fatalf("output %q missing %q", out, part)
		}
	}
}

func TestRemoteHostLinkSignsInAndListsTheMachineInTheAccount(t *testing.T) {
	f := &linkFixture{fixedAddresses: true, polls: []string{"authorization_pending", "ok"}}
	out, err := f.run(t, Deps{}, "--label", "build-vm")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.registrations) != 1 {
		t.Fatalf("registrations = %v, want one", f.registrations)
	}
	want := map[string]string{"label": "build-vm", "token": linkTestToken, "hostKey": linkTestHostKey}
	got := f.registrations[0]
	if len(got) != len(want) || got["label"] != want["label"] || got["token"] != want["token"] || got["hostKey"] != want["hostKey"] {
		t.Fatalf("registration = %v, want %v (no url: AO Cloud gives the fixed address)", got, want)
	}
	wantOutput(t, out, "user_code=ABCD-EFGH", `Linked "build-vm" to me@example.com`,
		"Address: https://"+linkFixedHost, "Cloudflare terminates TLS")
}

func TestRemoteHostLinkUsesTheQuickTunnelWhenTheAccountIsOutOfFixedAddresses(t *testing.T) {
	f := &linkFixture{fixedAddresses: true, conflictWithoutURL: true}
	out, err := f.run(t, Deps{}, "--label", "build-vm")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.registrations) != 2 || f.registrations[1]["url"] != "https://"+linkQuickHost {
		t.Fatalf("registrations = %v, want a retry at the quick tunnel", f.registrations)
	}
	// The address the machine is listed at, not a wait for a fixed one that never comes.
	wantOutput(t, out, "Address: https://"+linkQuickHost)
	if strings.Contains(out, "not ready") {
		t.Fatalf("output %q waited for a fixed address", out)
	}
}

func TestRemoteHostLinkStartsTheTunnelOnALanOnlyMachine(t *testing.T) {
	// LAN-only, on an AO Cloud without fixed addresses: the registration needs
	// the tunnel's address, so link must start the tunnel first.
	f := &linkFixture{enabled: true}
	out, err := f.run(t, Deps{}, "--label", "build-vm")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.registrations) != 1 || f.registrations[0]["url"] != "https://"+linkQuickHost {
		t.Fatalf("registrations = %v, want the quick tunnel's address", f.registrations)
	}
	wantOutput(t, out, "Cloudflare terminates TLS and can see the connection password and traffic.")
}

func TestRemoteHostLinkKeepsTheNameTheAccountHasForTheMachine(t *testing.T) {
	f := &linkFixture{enabled: true, loopbackOnly: true, tunnel: true, fixedAddresses: true,
		listed: `[{"hostId":"h_vm","label":"Desk name","token":"` + linkTestToken + `"}]`}
	out, err := f.run(t, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.registrations) != 1 || f.registrations[0]["label"] != "Desk name" {
		t.Fatalf("registrations = %v, want the account's name kept", f.registrations)
	}
	wantOutput(t, out, "already linked to me@example.com")

	f = &linkFixture{enabled: true, loopbackOnly: true, tunnel: true, fixedAddresses: true,
		listed: `[{"hostId":"h_vm","label":"Desk name","token":"` + linkTestToken + `"}]`}
	out, err = f.run(t, Deps{}, "--label", "build-vm")
	if err != nil {
		t.Fatal(err)
	}
	if f.registrations[0]["label"] != "build-vm" {
		t.Fatalf("registrations = %v, want --label to rename it", f.registrations)
	}
	wantOutput(t, out, `Linked "build-vm"`)
}

func TestRemoteHostLinkStopsWhenTheSignInIsNotFinished(t *testing.T) {
	for _, tc := range []struct {
		name  string
		polls []string
		want  string
	}{
		{"declined", []string{"authorization_pending", "access_denied"}, "declined"},
		{"expired by WorkOS", []string{"expired_token"}, "expired"},
		{"never approved", []string{"authorization_pending"}, "expired"},
		{"slowed down, never approved", []string{"slow_down", "authorization_pending"}, "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &linkFixture{fixedAddresses: true, polls: tc.polls}
			_, err := f.run(t, Deps{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.registrations) != 0 {
				t.Fatalf("registrations = %v, want none", f.registrations)
			}
		})
	}
}

func TestRemoteHostLinkSaysWhenTheFixedAddressIsNotReadyYet(t *testing.T) {
	f := &linkFixture{fixedAddresses: true, connectorStuck: true}
	out, err := f.run(t, Deps{}, "--label", "build-vm")
	if err != nil {
		t.Fatal(err)
	}
	// Listed in the account, so not a failure; the quick address is not the one to show.
	wantOutput(t, out, `Linked "build-vm"`, "not ready yet")
	if strings.Contains(out, linkQuickHost) {
		t.Fatalf("output %q shows the quick tunnel as the address", out)
	}
}

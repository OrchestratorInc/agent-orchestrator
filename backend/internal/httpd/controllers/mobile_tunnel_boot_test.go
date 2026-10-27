package controllers

import (
	"path/filepath"
	"testing"
	"time"
)

// The boot-time cloudflared lookup runs after the daemon starts listening. A
// status read in that gap must wait for the answer, not report remote access
// as unavailable: Connect Mobile stops polling once it has a status, so a wrong
// early answer would stick until the user reopened it.
func TestStatusWaitsForTheBootTunnelLookup(t *testing.T) {
	release := make(chan struct{})
	b := &BridgeService{ConfigPath: filepath.Join(t.TempDir(), "mobile.json"), LAN: &fakeLAN{}}
	b.ResolveTunnelInBackground(func() TunnelController {
		<-release
		return &fakeTunnel{}
	})

	got := make(chan bool, 1)
	go func() { got <- b.Status().Tunnel.Supported }()
	select {
	case <-got:
		t.Fatal("status answered before the boot lookup finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case supported := <-got:
		if !supported {
			t.Fatal("status reported remote access unsupported after the lookup found cloudflared")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("status never answered after the boot lookup finished")
	}
}

// Enabling during the boot lookup must use its connector rather than looking
// up and keeping a second one.
func TestEnableDuringBootLookupUsesItsConnector(t *testing.T) {
	release := make(chan struct{})
	boot := &fakeTunnel{}
	resolved := 0
	b := &BridgeService{
		ConfigPath:  filepath.Join(t.TempDir(), "mobile.json"),
		LAN:         &fakeLAN{},
		DefaultPort: 3011,
		ResolveTunnel: func() TunnelController {
			resolved++
			return &fakeTunnel{}
		},
	}
	b.ResolveTunnelInBackground(func() TunnelController {
		<-release
		return boot
	})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(release)
	}()

	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolver calls = %d, want 0: the boot lookup already found a connector", resolved)
	}
	if boot.startedOn != 3011 {
		t.Fatalf("boot connector started on %d, want the bound port 3011", boot.startedOn)
	}
}

// A boot lookup that finds nothing must not leave remote access off for good:
// enabling later looks again, which picks up a cloudflared installed since.
func TestEnableAfterAnEmptyBootLookupLooksAgain(t *testing.T) {
	tun := &fakeTunnel{}
	b := &BridgeService{
		ConfigPath:    filepath.Join(t.TempDir(), "mobile.json"),
		LAN:           &fakeLAN{},
		DefaultPort:   3011,
		ResolveTunnel: func() TunnelController { return tun },
	}
	b.ResolveTunnelInBackground(func() TunnelController { return nil })

	if b.Status().Tunnel.Supported {
		t.Fatal("nothing was found at boot, so remote access is not supported yet")
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if tun.startedOn != 3011 {
		t.Fatalf("connector started on %d, want the bound port 3011", tun.startedOn)
	}
}

package mobilebridge

import (
	"net"
	"testing"
)

func TestEndpointsListsEveryLANAddress(t *testing.T) {
	got := Endpoints(EndpointInputs{
		LANHosts: []string{"192.168.1.42", "10.0.0.5"},
		Port:     3011,
	})

	want := []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Secure: false},
		{Kind: KindLAN, Host: "10.0.0.5", Port: 3011, Secure: false},
	}
	assertEndpoints(t, got, want)
}

func assertEndpoints(t *testing.T, got, want []Endpoint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d endpoints %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("endpoint %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestEndpointsPutsTailscaleAfterLAN(t *testing.T) {
	got := Endpoints(EndpointInputs{
		LANHosts:       []string{"192.168.1.42"},
		TailscaleHosts: []string{"100.72.46.7"},
		Port:           3011,
	})

	want := []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Secure: false},
		{Kind: KindTailscale, Host: "100.72.46.7", Port: 3011, Secure: false},
	}
	assertEndpoints(t, got, want)
}

func TestEndpointsAppendsReadyTunnelLast(t *testing.T) {
	got := Endpoints(EndpointInputs{
		LANHosts: []string{"192.168.1.42"},
		Port:     3011,
		Tunnel:   &TunnelEndpoint{Ready: true, Hostname: "abc.trycloudflare.com"},
	})

	want := []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Secure: false},
		{Kind: KindTunnel, Host: "abc.trycloudflare.com", Port: 443, Secure: true},
	}
	assertEndpoints(t, got, want)
}

func TestEndpointsOmitsTunnelThatIsNotReady(t *testing.T) {
	got := Endpoints(EndpointInputs{
		LANHosts: []string{"192.168.1.42"},
		Port:     3011,
		Tunnel:   &TunnelEndpoint{Ready: false, Hostname: "abc.trycloudflare.com"},
	})

	assertEndpoints(t, got, []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Secure: false},
	})
}

func TestEndpointsIsEmptyWhenNothingIsReachable(t *testing.T) {
	got := Endpoints(EndpointInputs{Port: 3011})
	assertEndpoints(t, got, nil)
}

func TestEndpointsOmitsTunnelWithoutHostname(t *testing.T) {
	got := Endpoints(EndpointInputs{
		Port:   3011,
		Tunnel: &TunnelEndpoint{Ready: true, Hostname: ""},
	})
	assertEndpoints(t, got, nil)
}

func TestLocalEndpointsKeepsEveryCandidateFromEveryInterface(t *testing.T) {
	ifaces := []net.Interface{
		{Index: 1, Name: "lo0", Flags: net.FlagUp | net.FlagLoopback},
		{Index: 2, Name: "en0", Flags: net.FlagUp},
		{Index: 3, Name: "en1", Flags: net.FlagUp},
		{Index: 4, Name: "utun0", Flags: net.FlagUp},
	}
	addrs := map[string][]net.Addr{
		"lo0":   {cidr("127.0.0.1/8")},
		"en0":   {cidr("192.168.1.42/24")},
		"en1":   {cidr("10.0.0.5/24")},
		"utun0": {cidr("100.72.46.7/32")},
	}

	got := LocalEndpoints(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return addrs[i.Name], nil
	}, 3011, &TunnelEndpoint{Ready: true, Hostname: "abc.trycloudflare.com"})

	assertEndpoints(t, got, []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Secure: false},
		{Kind: KindLAN, Host: "10.0.0.5", Port: 3011, Secure: false},
		{Kind: KindTailscale, Host: "100.72.46.7", Port: 3011, Secure: false},
		{Kind: KindTunnel, Host: "abc.trycloudflare.com", Port: 443, Secure: true},
	})
}

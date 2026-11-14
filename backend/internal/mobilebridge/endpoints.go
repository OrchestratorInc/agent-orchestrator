package mobilebridge

import "net"

type EndpointKind string

const (
	KindLAN       EndpointKind = "lan"
	KindTailscale EndpointKind = "tailscale"
	KindTunnel    EndpointKind = "tunnel"
)

const tunnelPort = 443

type TunnelEndpoint struct {
	Ready    bool
	Hostname string
}

type Endpoint struct {
	Kind   EndpointKind `json:"kind"`
	Host   string       `json:"host"`
	Port   int          `json:"port"`
	Secure bool         `json:"secure"`
}

type EndpointInputs struct {
	LANHosts       []string
	TailscaleHosts []string
	Port           int
	Tunnel         *TunnelEndpoint
}

func Endpoints(in EndpointInputs) []Endpoint {
	var out []Endpoint
	if in.Port <= 0 {
		return nil
	}
	for _, h := range in.LANHosts {
		out = append(out, Endpoint{Kind: KindLAN, Host: h, Port: in.Port})
	}
	for _, h := range in.TailscaleHosts {
		out = append(out, Endpoint{Kind: KindTailscale, Host: h, Port: in.Port})
	}
	if in.Tunnel != nil && in.Tunnel.Ready && in.Tunnel.Hostname != "" {
		out = append(out, Endpoint{Kind: KindTunnel, Host: in.Tunnel.Hostname, Port: tunnelPort, Secure: true})
	}
	return out
}

func LocalEndpoints(
	ifaces []net.Interface,
	addrsOf func(net.Interface) ([]net.Addr, error),
	port int,
	tunnel *TunnelEndpoint,
) []Endpoint {
	return Endpoints(EndpointInputs{
		LANHosts:       PrivateIPv4Candidates(ifaces, addrsOf),
		TailscaleHosts: TailscaleIPv4Candidates(ifaces, addrsOf),
		Port:           port,
		Tunnel:         tunnel,
	})
}

func AdvertisedEndpoints(port int, tunnel *TunnelEndpoint) []Endpoint {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return LocalEndpoints(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	}, port, tunnel)
}

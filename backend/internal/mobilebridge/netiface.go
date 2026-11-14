package mobilebridge

import (
	"net"
	"strings"
)

var virtualNamePrefixes = []string{"utun", "tun", "tap", "docker", "bridge", "vmnet", "llw", "awdl"}

func hasVirtualName(name string) bool {
	n := strings.ToLower(name)
	for _, bad := range virtualNamePrefixes {
		if strings.HasPrefix(n, bad) {
			return true
		}
	}
	return false
}

func skipInterface(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
		return true
	}
	return hasVirtualName(i.Name)
}

var tailscaleCGNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isTunnelInterface(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
		return false
	}
	n := strings.ToLower(i.Name)
	return strings.HasPrefix(n, "utun") || strings.HasPrefix(n, "tun") || strings.HasPrefix(n, "tailscale")
}

func ipv4Candidates(
	ifaces []net.Interface,
	addrsOf func(net.Interface) ([]net.Addr, error),
	keepIface func(net.Interface) bool,
	keepIP func(net.IP) bool,
) []string {
	var out []string
	for _, i := range ifaces {
		if !keepIface(i) {
			continue
		}
		addrs, err := addrsOf(i)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if keepIP(ip4) {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}

func PrivateIPv4Candidates(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) []string {
	return ipv4Candidates(ifaces, addrsOf,
		func(i net.Interface) bool { return !skipInterface(i) },
		func(ip net.IP) bool { return ip.IsPrivate() },
	)
}

func AutopickLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	c := PrivateIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
	if len(c) == 0 {
		return ""
	}
	return c[0]
}

func TailscaleIPv4Candidates(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) []string {
	return ipv4Candidates(ifaces, addrsOf, isTunnelInterface, tailscaleCGNAT.Contains)
}

func AutopickTailscaleIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	c := TailscaleIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
	if len(c) == 0 {
		return ""
	}
	return c[0]
}

func LocalPrivateIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return PrivateIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
}

func LocalTailscaleIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return TailscaleIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
}

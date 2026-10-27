package domain

import "time"

// MaxRemoteHostsPerAccount caps the machines one account can list. Each
// listed machine can hold a tunnel and a DNS record in the shared zone, so
// the cap keeps one account from filling it.
const MaxRemoteHostsPerAccount = 10

// RemoteHost is an account-owned self-hosted machine. The separate host token
// grants access only to this machine; the pairing password stays on the host.
type RemoteHost struct {
	HostID         string
	UserID         string
	Label          string
	URL            string
	EncryptedToken []byte
	TokenNonce     []byte
	UpdatedAt      time.Time
}

// RemoteHostTunnel is the Cloudflare tunnel that gives a host its fixed
// address in one account. Each account that lists the host has its own, so
// listing another account's host ID never reaches that account's tunnel.
// CreatedAt is when its DNS record was made. Released means the tunnel was
// deleted while the host was offline: its DNS record still names TunnelID, and
// the host gets a new tunnel under it when it asks again.
type RemoteHostTunnel struct {
	UserID            string
	HostID            string
	TunnelID          string
	Hostname          string
	EncryptedRunToken []byte
	RunTokenNonce     []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastSeenAt        time.Time
	Released          bool
}

// RemoteHostTunnelCandidate is a tunnel the sweep may delete: Registered is
// false when its account no longer lists its host.
type RemoteHostTunnelCandidate struct {
	HostID     string
	TunnelID   string
	Hostname   string
	LastSeenAt time.Time
	Registered bool
	Released   bool
}

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

// RemoteHostTunnel is the Cloudflare tunnel that gives one host its fixed
// address. It belongs to the host, not to an account.
type RemoteHostTunnel struct {
	HostID            string
	TunnelID          string
	Hostname          string
	EncryptedRunToken []byte
	RunTokenNonce     []byte
	UpdatedAt         time.Time
	LastSeenAt        time.Time
}

// RemoteHostTunnelCandidate is a tunnel the sweep may delete: Registered is
// false when no account lists its host any more.
type RemoteHostTunnelCandidate struct {
	HostID     string
	TunnelID   string
	Hostname   string
	LastSeenAt time.Time
	Registered bool
}

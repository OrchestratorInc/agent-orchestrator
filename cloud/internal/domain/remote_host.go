package domain

import "time"

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

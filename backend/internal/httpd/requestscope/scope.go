// Package requestscope carries transport facts that cannot be trusted from
// client-controlled headers. The LAN listener marks requests before they enter
// the shared API router.
package requestscope

import "context"

type lanKey struct{}

// WithLAN marks a request context as arriving through the authenticated LAN
// listener rather than the loopback desktop listener.
func WithLAN(ctx context.Context) context.Context {
	return context.WithValue(ctx, lanKey{}, true)
}

// IsLAN reports whether the physical LAN listener marked this request.
func IsLAN(ctx context.Context) bool {
	value, _ := ctx.Value(lanKey{}).(bool)
	return value
}

// CredentialKind names what authenticated a request on the LAN listener.
type CredentialKind string

// The credentials the LAN listener accepts. A pairing code counts only for pairing.
const (
	CredentialPassword    CredentialKind = "password"
	CredentialAccount     CredentialKind = "account"
	CredentialDevice      CredentialKind = "device"
	CredentialPairingCode CredentialKind = "pairing-code"
)

// Credential is what the LAN listener accepted. DeviceID is set for CredentialDevice.
type Credential struct {
	Kind     CredentialKind
	DeviceID string
}

type credentialKey struct{}

// WithCredential records the credential the LAN listener accepted.
func WithCredential(ctx context.Context, credential Credential) context.Context {
	return context.WithValue(ctx, credentialKey{}, credential)
}

// CredentialFrom returns the accepted credential, or the zero Credential for a loopback request.
func CredentialFrom(ctx context.Context) Credential {
	credential, _ := ctx.Value(credentialKey{}).(Credential)
	return credential
}

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

const (
	// CredentialPassword is the pairing password every hand-paired device shares.
	CredentialPassword CredentialKind = "password"
	// CredentialAccount is the account token every signed-in device shares.
	CredentialAccount CredentialKind = "account"
	// CredentialDevice is one device's own token.
	CredentialDevice CredentialKind = "device"
	// CredentialPairingCode is a one-time code, accepted only for pairing.
	CredentialPairingCode CredentialKind = "pairing-code"
)

// Credential is the credential the LAN listener accepted for a request.
// DeviceID is set for CredentialDevice.
type Credential struct {
	Kind     CredentialKind
	DeviceID string
}

type credentialKey struct{}

// WithCredential records the credential the LAN listener accepted.
func WithCredential(ctx context.Context, credential Credential) context.Context {
	return context.WithValue(ctx, credentialKey{}, credential)
}

// CredentialFrom returns the credential the LAN listener accepted, or the
// zero Credential for a request it did not authenticate, such as a loopback one.
func CredentialFrom(ctx context.Context) Credential {
	credential, _ := ctx.Value(credentialKey{}).(Credential)
	return credential
}

package requestscope

import "context"

type lanKey struct{}

func WithLAN(ctx context.Context) context.Context {
	return context.WithValue(ctx, lanKey{}, true)
}

func IsLAN(ctx context.Context) bool {
	value, _ := ctx.Value(lanKey{}).(bool)
	return value
}

type CredentialKind string

const (
	CredentialPassword    CredentialKind = "password"
	CredentialAccount     CredentialKind = "account"
	CredentialDevice      CredentialKind = "device"
	CredentialPairingCode CredentialKind = "pairing-code"
)

type Credential struct {
	Kind     CredentialKind
	DeviceID string
}

type credentialKey struct{}

func WithCredential(ctx context.Context, credential Credential) context.Context {
	return context.WithValue(ctx, credentialKey{}, credential)
}

func CredentialFrom(ctx context.Context) Credential {
	credential, _ := ctx.Value(credentialKey{}).(Credential)
	return credential
}

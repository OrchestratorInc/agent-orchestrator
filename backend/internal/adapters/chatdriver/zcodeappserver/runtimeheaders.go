package zcodeappserver

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
)

// runtimeHeadersResolver answers interaction/requestProviderRuntimeHeaders.
//
// This is the U0 seam (DESIGN 7.1): statically, the app server asks the client
// for model credentials before every model request on the zhipu-account path,
// and refuses the request unless headersApplied and requestAuth come back. Whether
// that request fires for account:zai-individual-coding-plan, and what a decline
// does to the turn, is the gating Phase 1 question. Phase 0 ships only the
// decline; Phase 1 decides between a Go-side mint, a GLM-side answer, or
// nothing.
type runtimeHeadersResolver interface {
	Resolve(ctx context.Context, params zcodeproto.RequestProviderRuntimeHeadersParams) (zcodeproto.RequestProviderRuntimeHeadersResult, error)
}

// declineResolver is the Phase 0 default: refuse with -32601 so the server
// treats the request as unsupported rather than as a malformed answer. It
// never sees, logs or forwards a credential.
type declineResolver struct{}

func (declineResolver) Resolve(context.Context, zcodeproto.RequestProviderRuntimeHeadersParams) (zcodeproto.RequestProviderRuntimeHeadersResult, error) {
	return zcodeproto.RequestProviderRuntimeHeadersResult{}, &rpcError{
		Code:    zcodeproto.CodeMethodNotFound,
		Message: "provider runtime headers are not supplied by agent-orchestrator (U0)",
	}
}

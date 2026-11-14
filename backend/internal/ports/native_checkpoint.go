package ports

import "context"

type NativeCheckpointVerifier interface {
	VerifyNativeCheckpoint(context.Context, NativeCheckpointRequest) (NativeCheckpointBoundary, error)
}

type NativeCheckpointRequest struct {
	ProviderConversationID string
	Env                    map[string]string
	Evidence               string
}

type NativeCheckpointBoundary struct {
	UserMessageID string
	UserText      string
	AssistantText string
	Interrupted   bool
}

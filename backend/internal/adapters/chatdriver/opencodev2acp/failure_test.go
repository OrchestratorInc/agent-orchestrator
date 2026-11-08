package opencodev2acp

import (
	"errors"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestPromptErrorFailureRestatesProviderTransport(t *testing.T) {
	const want = "OpenCode could not connect to the model server. Check that the server is running and that the URL in your OpenCode provider config is reachable from this computer.\n\nFailedToOpenSocket: Was there a typo in the url or port?"
	transport := func(data map[string]any) error {
		return &acpsdk.RequestError{Code: -32603, Message: "Internal error: FailedToOpenSocket: Was there a typo in the url or port?", Data: data}
	}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "error relayed by AO's chat host",
			err: transport(map[string]any{
				"ao.persistentEventId": "acp-host:1:196",
				"providerData":         map[string]any{"errorName": "provider.transport", "service": "session"},
			}),
			want: want,
		},
		{name: "unwrapped agent error", err: transport(map[string]any{"errorName": "provider.transport"}), want: want},
		{name: "another provider error keeps the generic error", err: transport(map[string]any{"providerData": map[string]any{"errorName": "provider.auth"}})},
		{name: "an error without data keeps the generic error", err: acpsdk.NewInternalError(nil)},
		{name: "another error code keeps the generic error", err: &acpsdk.RequestError{Code: -32602, Message: "Invalid params", Data: map[string]any{"errorName": "provider.transport"}}},
		{name: "a non-ACP error keeps the generic error", err: errors.New("provider.transport")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promptErrorFailure(tt.err, false)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("promptErrorFailure = %q, want nil", got)
				}
				return
			}
			if got == nil || got.Error() != tt.want {
				t.Fatalf("promptErrorFailure = %v, want %q", got, tt.want)
			}
			var requestErr *acpsdk.RequestError
			if !errors.As(got, &requestErr) {
				t.Fatalf("restated failure dropped the agent error: %#v", got)
			}
		})
	}
}

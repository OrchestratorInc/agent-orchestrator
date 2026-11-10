package acp

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// TestDiscoverModelsKeepsPerModelEfforts 验证真实协议转换不会把默认模型能力复制给其他模型。
func TestDiscoverModelsKeepsPerModelEfforts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response []acpsdk.SessionConfigOption
		want     []string
	}{
		{"different", []acpsdk.SessionConfigOption{
			selectConfigOption("model", "Model", "model", "custom", "native", "custom"),
			selectConfigOption("reasoning_effort", "Effort", "thought_level", "medium", "", "minimal", "medium", "xhigh"),
		}, []string{"minimal", "medium", "xhigh"}},
		{"no-effort", []acpsdk.SessionConfigOption{
			selectConfigOption("model", "Model", "model", "custom", "native", "custom"),
		}, nil},
		{"stale-response", []acpsdk.SessionConfigOption{
			selectConfigOption("model", "Model", "model", "native", "native", "custom"),
			selectConfigOption("reasoning_effort", "Effort", "thought_level", "high", "low", "high"),
		}, nil},
		{"empty-response", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &fakeAgent{newConfig: []acpsdk.SessionConfigOption{
				selectConfigOption("model", "Model", "model", "native", "native", "custom"),
				selectConfigOption("reasoning_effort", "Effort", "thought_level", "high", "low", "high"),
			}, setConfig: tc.response}
			driver := New(Config{Launch: func(context.Context, LaunchConfig) (Launch, error) {
				return Launch{Command: "fake"}, nil
			}}, slog.New(slog.DiscardHandler))
			driver.useTestProcess(fakeSpawn(agent))
			got, err := driver.discoverModels(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || !reflect.DeepEqual(got[0].Efforts, []string{"low", "high"}) || got[0].DefaultEffort != "high" || !got[0].IsDefault {
				t.Fatalf("default model metadata = %+v", got)
			}
			if !reflect.DeepEqual(got[1].Efforts, tc.want) || got[1].IsDefault {
				t.Fatalf("selected model = %+v, want efforts %v", got[1], tc.want)
			}
			agent.mu.Lock()
			defer agent.mu.Unlock()
			if agent.promptParams.Prompt != nil || agent.setCalls != 1 || agent.options["model"] != "custom" || agent.closeCalls != 1 {
				t.Fatalf("unexpected probe: prompt=%v calls=%d options=%v closes=%d", agent.promptParams, agent.setCalls, agent.options, agent.closeCalls)
			}
		})
	}
}

// TestDiscoverModelsFailurePreservesCatalog 确认模型切换失败时仍保留列表但不虚报其档位。
func TestDiscoverModelsFailurePreservesCatalog(t *testing.T) {
	agent := &fakeAgent{newConfig: []acpsdk.SessionConfigOption{
		selectConfigOption("model", "Model", "model", "native", "native", "unavailable"),
	}, configErr: io.EOF}
	driver := New(Config{Launch: func(context.Context, LaunchConfig) (Launch, error) {
		return Launch{Command: "fake"}, nil
	}}, slog.New(slog.DiscardHandler))
	driver.useTestProcess(fakeSpawn(agent))
	got, err := driver.discoverModels(context.Background(), t.TempDir())
	if err != nil || len(got) != 2 || len(got[1].Efforts) != 0 {
		t.Fatalf("models=%+v err=%v", got, err)
	}
}

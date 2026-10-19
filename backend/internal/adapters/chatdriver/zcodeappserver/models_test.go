package zcodeappserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestListModelsReadsSnapshotSettings(t *testing.T) {
	conv, _ := startConversation(t)
	models, err := conv.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ID != "zai-api-ao/GLM-5.3" || !models[0].Default || models[0].DisplayName != "GLM-5.3" {
		t.Fatalf("models[0] = %+v", models[0])
	}
	// The reasoning levels are the efforts (PROVEN: a selection must carry one).
	if !reflect.DeepEqual(models[0].Efforts, []string{"low", "high", "max"}) || models[0].DefaultEffort != "max" {
		t.Fatalf("models[0] efforts = %v default %q", models[0].Efforts, models[0].DefaultEffort)
	}
	if models[1].ID != "zai-api-ao/GLM-5.3-Flash" || models[1].Default || models[1].DefaultEffort != "high" {
		t.Fatalf("models[1] = %+v", models[1])
	}
}

func TestListModelsFromTheCapturedSnapshot(t *testing.T) {
	conv, srv := startConversation(t)
	// The p7 session/read result is the authoritative shape (p7-models.ndjson).
	srv.respondTo(zcodeproto.MethodSessionRead, fixtureResult(t, "p7-models.ndjson", 3))
	if _, err := conv.readSnapshot(context.Background(), 1); err != nil {
		t.Fatalf("readSnapshot: %v", err)
	}
	models, err := conv.ListModels(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
	if models[0].ID != "zai-api-ao/GLM-5.3" || !models[0].Default || !reflect.DeepEqual(models[0].Efforts, []string{"low", "high", "max"}) || models[0].DefaultEffort != "max" {
		t.Fatalf("models[0] = %+v", models[0])
	}
}

func TestLaunchEffortRidesOnEveryTurnAndIsTheDefaultEffort(t *testing.T) {
	d, srv := newTestDriver(t)
	cfg := startConfig(t)
	cfg.Effort = "low"
	started, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = started.Close() }()
	if _, ok := paramsMap(t, srv.awaitRequest(zcodeproto.MethodSessionCreate))["thoughtLevel"]; ok {
		t.Fatal("session/create carries the STATIC thoughtLevel")
	}
	conv := started.(*conversation)
	models, _ := conv.ListModels(context.Background())
	if models[0].DefaultEffort != "low" || models[1].DefaultEffort != "high" {
		t.Fatalf("default efforts = %q / %q, want the launch level on the current model only", models[0].DefaultEffort, models[1].DefaultEffort)
	}
	sendTurn(t, conv, "x")
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	var params zcodeproto.SessionSendParams
	if err := json.Unmarshal(send.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.ModelSelection == nil || params.ModelSelection.ModelID != "GLM-5.3" || params.ModelSelection.Options == nil || params.ModelSelection.Options.ReasoningLevel != "low" {
		t.Fatalf("modelSelection = %+v, want the current model at the launch level", params.ModelSelection)
	}
}

// A launch level the catalog does not offer is refused at Start, where the
// user can change the flag, instead of being substituted into every turn and
// refused there (a session that exists but can never send).
func TestStartRefusesALaunchEffortTheModelDoesNotOffer(t *testing.T) {
	d, srv := newTestDriver(t)
	cfg := startConfig(t)
	cfg.Effort = "ultra"
	conv, err := d.Start(context.Background(), cfg)
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) || !strings.Contains(err.Error(), "ultra") {
		t.Fatalf("Start with an unoffered launch effort = %v, want ErrChatConfigOptionInvalid", err)
	}
	if conv != nil {
		t.Fatal("a refused start returned a conversation")
	}
	if !srv.sentMethod(zcodeproto.MethodSessionCreate) {
		t.Fatal("the refusal must come from the create snapshot's catalog, after session/create")
	}
}

// A stored session is never stranded by its launch level: Resume keeps the
// session usable at the model's own level and logs the substitution.
func TestResumeDropsALaunchEffortTheModelDoesNotOffer(t *testing.T) {
	d, srv := newTestDriver(t)
	resumed, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), ProviderConversationID: "sess-1",
		Permissions: ports.PermissionModeBypassPermissions, HasPriorTurns: true, Effort: "ultra",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = resumed.Close() }()
	conv := resumed.(*conversation)
	models, _ := conv.ListModels(context.Background())
	if len(models) == 0 || models[0].DefaultEffort != "max" {
		t.Fatalf("default effort = %+v, want the model's own level once the launch level is dropped", models)
	}
	sendTurn(t, conv, "x")
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	if _, ok := paramsMap(t, send)["modelSelection"]; ok {
		t.Fatalf("session/send still carries the unoffered launch level: %s", send.Params)
	}
}

func TestPerTurnEffortOverridesTheLaunchLevel(t *testing.T) {
	conv, srv := startConversation(t)
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "x", Settings: ports.ChatTurnSettings{Effort: "high"}})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	if !strings.Contains(string(send.Params), `"modelSelection":{"providerId":"zai-api-ao","modelId":"GLM-5.3","options":{"reasoningLevel":"high"}}`) {
		t.Fatalf("session/send params = %s", send.Params)
	}
	// A level the model does not offer is refused before anything is written.
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "x", Settings: ports.ChatTurnSettings{Effort: "ultra"}}); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("SendTurn with an unknown level = %v, want ErrChatConfigOptionInvalid", err)
	}
}

func TestTurnWithoutSettingsSendsNoModelSelection(t *testing.T) {
	conv, srv := startConversation(t)
	sendTurn(t, conv, "x")
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	if _, ok := paramsMap(t, send)["modelSelection"]; ok {
		t.Fatalf("session/send carries a modelSelection with nothing chosen: %s", send.Params)
	}
}

func TestListModelsWithoutSnapshotIsEmpty(t *testing.T) {
	conv := &conversation{}
	models, err := conv.ListModels(context.Background())
	if err != nil || len(models) != 0 {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
}

func TestParseModelSelectionSplitsOnLastSlash(t *testing.T) {
	cases := []struct {
		id       string
		provider string
		model    string
		ok       bool
	}{
		{"account:zai-individual-coding-plan/GLM-5.3", "account:zai-individual-coding-plan", "GLM-5.3", true},
		{"zai-api-ao/GLM-5.3-Flash", "zai-api-ao", "GLM-5.3-Flash", true},
		{"vendor/sub/model-x", "vendor/sub", "model-x", true},
		{"no-slash", "", "", false},
		{"/leading", "", "", false},
		{"trailing/", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		got, ok := parseModelSelection(tc.id)
		if ok != tc.ok || got.ProviderID != tc.provider || got.ModelID != tc.model {
			t.Errorf("parseModelSelection(%q) = %+v, %v; want %q/%q, %v", tc.id, got, ok, tc.provider, tc.model, tc.ok)
		}
	}
	if modelID(zcodeproto.ModelSelection{ProviderID: "a:b", ModelID: "c"}) != "a:b/c" {
		t.Fatal("modelID join")
	}
}

func sendWithModel(model string) ports.ChatUserMessage {
	return ports.ChatUserMessage{Text: "which model?", Settings: ports.ChatTurnSettings{Model: model}}
}

func TestSendTurnCarriesModelSelectionWithTheModelsDefaultLevel(t *testing.T) {
	conv, srv := startConversation(t)
	ref, err := conv.SendTurn(context.Background(), sendWithModel("zai-api-ao/GLM-5.3-Flash"))
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	var params zcodeproto.SessionSendParams
	if err := json.Unmarshal(send.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	// PROVEN (p7-models.ndjson): a modelSelection needs options.reasoningLevel;
	// with no effort chosen the model's own defaultLevel is used.
	if params.ModelSelection == nil || params.ModelSelection.ProviderID != "zai-api-ao" || params.ModelSelection.ModelID != "GLM-5.3-Flash" ||
		params.ModelSelection.Options == nil || params.ModelSelection.Options.ReasoningLevel != "high" {
		t.Fatalf("modelSelection = %+v", params.ModelSelection)
	}
	// The captured send is the reference shape.
	captured := fixtureFrame(t, "p7-models.ndjson", func(line replayLine) bool {
		return line.Dir == "out" && line.Frame.Method == zcodeproto.MethodSessionSend
	})
	var want zcodeproto.SessionSendParams
	if err := json.Unmarshal(captured.Params, &want); err != nil {
		t.Fatalf("captured params: %v", err)
	}
	if want.ModelSelection == nil || want.ModelSelection.Options == nil || want.ModelSelection.Options.ReasoningLevel == "" {
		t.Fatalf("captured modelSelection = %+v", want.ModelSelection)
	}
	// A model the session does not offer is refused up front.
	if _, err := conv.SendTurn(context.Background(), sendWithModel("account:zai-individual-coding-plan/GLM-5.3")); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("SendTurn with an unoffered model = %v, want ErrChatConfigOptionInvalid", err)
	}
}

func TestSetModelSendsSessionSetModelWithAReasoningLevel(t *testing.T) {
	conv, srv := startConversation(t)
	srv.respondTo(zcodeproto.MethodSessionSetModel, fixtureResult(t, "p7-models.ndjson", 4))
	if err := conv.SetModel(context.Background(), "zai-api-ao/GLM-5.3-Flash"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	set := srv.awaitRequest(zcodeproto.MethodSessionSetModel)
	var params zcodeproto.SessionSetModelParams
	if err := json.Unmarshal(set.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.SessionID != "sess-1" || params.Model.ModelID != "GLM-5.3-Flash" || params.Model.Options == nil || params.Model.Options.ReasoningLevel != "high" {
		t.Fatalf("params = %+v", params)
	}
	// The returned snapshot (captured) replaces the catalog.
	models, _ := conv.ListModels(context.Background())
	if len(models) != 1 || models[0].ID != "zai-api-ao/GLM-5.3-Flash" || !models[0].Default {
		t.Fatalf("models after setModel = %+v", models)
	}
	if err := conv.SetModel(context.Background(), "bogus"); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("bogus id = %v", err)
	}
}

func TestModelChangedPatchRefreshesTheCatalog(t *testing.T) {
	conv, srv := startConversation(t)
	patch := fixtureFrame(t, "p7-models.ndjson", func(line replayLine) bool {
		return line.Dir == "in" && line.Frame.Method == zcodeproto.MethodStateUpdated && strings.Contains(string(line.Frame.Params), "model_changed")
	})
	srv.push(marshalFrame(t, patch))
	deadline := time.After(2 * time.Second)
	for {
		models, _ := conv.ListModels(context.Background())
		if len(models) == 1 && models[0].ID == "zai-api-ao/GLM-5.3-Flash" && models[0].Default {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("catalog not refreshed from model_changed: %+v", models)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

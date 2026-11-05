package awake

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type deferredCapableHost struct{ *fakeHost }

func (h *deferredCapableHost) Capabilities() []domain.AwakeCapability {
	result := h.fakeHost.Capabilities()
	for _, id := range []string{"audio", "keyboard", "display_restore", "pause_apps", "hotspot", "agent_reconnect", "browser_actions", "display_awake", "low_power"} {
		result = append(result, domain.AwakeCapability{ID: id, Available: true})
	}
	return result
}

func TestDeferredSettingsRejectedEvenOnCapableHost(t *testing.T) {
	for name, enable := range map[string]func(*domain.AwakeSettings){
		"muteAudio":       func(s *domain.AwakeSettings) { s.MuteAudio = true },
		"darkenKeyboard":  func(s *domain.AwakeSettings) { s.DarkenKeyboard = true },
		"restoreDisplay":  func(s *domain.AwakeSettings) { s.RestoreDisplay = true },
		"pauseApps":       func(s *domain.AwakeSettings) { s.PauseApps = []string{"example.app"} },
		"hotspotName":     func(s *domain.AwakeSettings) { s.HotspotName = "saved" },
		"hotspotRecovery": func(s *domain.AwakeSettings) { s.HotspotRecovery = true; s.HotspotName = "saved" },
		"agentReconnect":  func(s *domain.AwakeSettings) { s.AgentReconnect = true },
		"browserActions":  func(s *domain.AwakeSettings) { s.BrowserActions = true },
	} {
		t.Run(name, func(t *testing.T) {
			s, store, h, b, _ := fixture(t)
			s.host = &deferredCapableHost{h}
			b.recovery = true
			h.observation.KnownNetworks = []string{"saved"}
			s.refresh(context.Background())
			settings := s.Status().Settings
			enable(&settings)
			err := s.UpdateSettings(context.Background(), settings)
			if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "deferred") {
				t.Fatalf("deferred setting accepted or rejected for the wrong reason: %v", err)
			}
			if store.writes != 0 || h.active || h.browserCalls != 0 || b.reconnectCalls != 0 {
				t.Fatal("rejected settings changed durable state or activated host work")
			}
		})
	}
}

func TestStartupDisablesPersistedDeferredPoliciesBeforeActivation(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "save-failed"}[failSave], func(t *testing.T) {
			s, store, h, b, now := fixture(t)
			b.agent, b.recovery = true, true
			s.doc.Settings = domain.AwakeSettings{DisplayAwake: true, StopBatteryPercent: 20, LowPowerBatteryPercent: 30, LowPowerOnSeriousHeat: true,
				MuteAudio: true, DarkenKeyboard: true, RestoreDisplay: true, PauseApps: []string{"example.app"},
				HotspotRecovery: true, HotspotName: "saved", AgentReconnect: true, BrowserActions: true}
			s.doc.Rules = []domain.AwakeRule{{ID: "work", Name: "Agent work", Enabled: true, Match: "all", Conditions: []domain.AwakeCondition{{Kind: "agent_activity"}}}}
			data, err := json.Marshal(s.doc)
			if err != nil {
				t.Fatal(err)
			}
			store.document, store.fail = string(data), failSave
			next, err := New(context.Background(), store, &deferredCapableHost{h}, b)
			if failSave {
				if err == nil || next != nil || h.active {
					t.Fatal("startup admitted unsupported saved policies after reset save failed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			next.now = s.now
			var persisted domain.AwakeDocument
			if err := json.Unmarshal([]byte(store.document), &persisted); err != nil {
				t.Fatal(err)
			}
			settings := persisted.Settings
			if settings.MuteAudio || settings.DarkenKeyboard || settings.RestoreDisplay || len(settings.PauseApps) != 0 || settings.HotspotRecovery || settings.HotspotName != "" || settings.AgentReconnect || settings.BrowserActions || store.writes != 1 {
				t.Fatal("persisted deferred opt-ins survived startup")
			}
			if !settings.DisplayAwake || settings.StopBatteryPercent != 20 || settings.LowPowerBatteryPercent != 30 || !settings.LowPowerOnSeriousHeat {
				t.Fatal("startup reset changed supported settings")
			}
			tick(t, next)
			*now = now.Add(5 * time.Second)
			tick(t, next)
			if !h.active {
				t.Fatal("scope guard disabled supported automatic awake rules")
			}
			if next.networkTask(context.Background()) != nil || next.agentTask(context.Background()) != nil {
				t.Fatal("saved deferred policy scheduled host actions")
			}
		})
	}
}

func TestDeferredCapabilitiesNeverAdvertiseAvailability(t *testing.T) {
	s, _, h, b, _ := fixture(t)
	s.host = &deferredCapableHost{h}
	b.recovery = true
	for _, cap := range s.Status().Capabilities {
		switch cap.ID {
		case "audio", "keyboard", "display_restore", "pause_apps", "hotspot", "agent_reconnect", "browser_actions":
			if cap.Available || !strings.Contains(cap.Reason, "deferred") {
				t.Fatalf("deferred capability advertised: %+v", cap)
			}
		}
	}
}

func TestSupportedSettingsRemainAvailable(t *testing.T) {
	s, store, h, _, _ := fixture(t)
	s.host = &deferredCapableHost{h}
	settings := s.Status().Settings
	settings.LidClosed = false
	settings.DisplayAwake = true
	settings.StopBatteryPercent = 20
	settings.LowPowerBatteryPercent = 30
	settings.LowPowerOnSeriousHeat = true
	if err := s.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	actual := s.Status().Settings
	if store.writes != 1 || actual.LidClosed || !actual.DisplayAwake || actual.StopBatteryPercent != 20 || actual.LowPowerBatteryPercent != 30 || !actual.LowPowerOnSeriousHeat {
		t.Fatal("scope guard rejected or changed supported settings")
	}
}

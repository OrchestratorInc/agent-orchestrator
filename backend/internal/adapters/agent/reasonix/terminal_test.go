package reasonix

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReasonixComposerReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, screen string
		ready        bool
	}{
		{"ready", "Reasonix\n────────────────\n❯ \n────────────────\nRead only · ready · Shift+Tab read-only/workspace/YOLO/plan · Ctrl+Y YOLO\nMODEL deepseek", true},
		{"Chinese", "──────\n❯ \n──────\nWorkspace · 就绪 · Shift+Tab 仅可查看/工作区内修改/YOLO/计划 · Ctrl+Y YOLO", true},
		{"YOLO", "──────\n❯ \n──────\nYOLO · Shift+Tab read-only/workspace/YOLO/plan · Ctrl+Y YOLO", true},
		{"Git and telemetry", "──────\n❯ \n──────\nRead only · ready · Shift+Tab read-only/workspace/YOLO/plan · Ctrl+Y YOLO\nMODEL fake/model-a\n────────────────\nrepo@feature/reasonix  +2 -1 ?3  CTX 4k/128k  COMPACT 96%", true},
		{"wrapped shortcuts", "──────\n❯ \n──────\nRead only · ready\nShift+Tab read-only/workspace/YOLO/plan\nCtrl+Y YOLO\nMODEL fake/model-a\n────────\nrepo@main", true},
		{"Chinese telemetry", "──────\n❯ \n──────\nWorkspace · 就绪 · Shift+Tab 仅可查看/工作区内修改/YOLO/计划 · Ctrl+Y YOLO\n模型 fake/model-a\n────────\n上下文 4k/128k  压缩 96%", true},
		{"Traditional telemetry", "──────\n❯ \n──────\nWorkspace · 就緒 · Shift+Tab 僅可查看/工作區內修改/YOLO/計畫 · Ctrl+Y YOLO\n模型 fake/model-a\n────────\n快取 50%  費用 $0", true},
		{"unknown content after data band", "──────\n❯ \n──────\nRead only · ready · Shift+Tab read-only/workspace/YOLO/plan\n────────\nPermission required: approve?", false},
		{"draft", "──────\n❯ my unsent draft\n──────\nRead only · ready · Shift+Tab read-only/workspace/YOLO/plan", false},
		{"startup", "Reasonix starting...", false},
		{"approval", "──────\n❯ \n──────\nRead only · 1 approve once · n/Esc deny · Ctrl-C cancels turn", false},
		{"working", "⠋ thinking… (1s · Esc cancels)\n──────\n❯ \n──────\nRead only · ready · Shift+Tab read-only/workspace/YOLO/plan", false},
		{"historical composer", "❯ \nRead only · ready · Shift+Tab\npermission required\n1. Allow once\n2. Deny\nChoose [1/2]", false},
		{"shell error", "error: unknown flag --append-system-prompt-file\n❯ ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := New().DetectTerminalActivity(tc.screen)
			if (ok && state == domain.ActivityIdle) != tc.ready {
				t.Fatalf("state=%s ok=%v", state, ok)
			}
		})
	}
	hints, err := New().PromptReadinessHints(t.Context(), ports.LaunchConfig{})
	if err != nil || !hints.RequireReady || hints.Timeout <= 0 {
		t.Fatalf("unsafe readiness: %+v %v", hints, err)
	}
}

package usage

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	ompTitleSlotRecord    = `{"type":"title","v":1,"title":"Fix the flaky watcher test","source":"auto","updatedAt":"2026-08-28T14:17:13.741Z","pad":"                                        "}`
	ompSessionRecord      = `{"type":"session","version":3,"id":"01a048bb-551d-7000-8b7e-1a07961198e0","timestamp":"2026-08-28T14:17:11.709Z","cwd":"/repo","title":"Fix the flaky watcher test","titleSource":"auto"}`
	ompZaiCachedReply     = `{"type":"message","id":"f7bd8620","parentId":"281bf2e2","timestamp":"2026-08-28T14:25:56.303Z","message":{"role":"assistant","api":"anthropic-messages","provider":"zai","model":"glm-5.2","usage":{"input":472,"output":426,"cacheRead":40896,"cacheWrite":0,"totalTokens":41794,"cost":{"input":0.0006608,"output":0.0018744,"cacheRead":0.01063296,"cacheWrite":0,"total":0.01316816}},"stopReason":"stop","timestamp":1787927144753,"responseId":"msg_2026082822254498a08bd741d24af9","duration":11525.8,"ttft":2882.09}}`
	ompFailedReply        = `{"type":"message","id":"9679084c","parentId":"0d7035fe","timestamp":"2026-09-17T14:14:21.110Z","message":{"role":"assistant","api":"anthropic-messages","provider":"zai","model":"glm-5.1","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","timestamp":1789654460673,"errorStatus":401,"errorId":16781312,"duration":361.68,"completedAt":1789654461065}}`
	ompFailedJudgeCall    = `{"type":"model_usage","id":"d55325cf","parentId":"59d358f8","timestamp":"2026-10-19T12:15:37.062Z","runtime":{"version":"18.12.1"},"purpose":"auto-thinking","role":"judge","api":"anthropic-messages","provider":"zai","model":"glm-5.2","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error"}`
	ompJudgeCall          = `{"type":"model_usage","id":"8f5be1cb","parentId":"b025bec6","timestamp":"2026-10-19T12:17:46.134Z","runtime":{"version":"18.12.1"},"purpose":"auto-thinking","role":"judge","api":"anthropic-messages","provider":"zai","model":"glm-5.2","usage":{"input":812,"output":9,"cacheRead":0,"cacheWrite":0,"totalTokens":821,"cost":{"input":0.0011368,"output":0.0000396,"cacheRead":0,"cacheWrite":0,"total":0.0011764}},"stopReason":"stop"}`
	kimchiSessionRecord   = `{"type":"session","version":3,"id":"019ffc6e-fcb3-7f68-a162-85693427d059","timestamp":"2026-08-13T18:42:39.923Z","cwd":"/repo"}`
	kimchiGatewayReply    = `{"type":"message","id":"c662d70c","parentId":"f618ed94","timestamp":"2026-08-26T09:33:24.569Z","message":{"role":"assistant","api":"openai-completions","provider":"kimchi-dev","model":"kimi-k2.7","usage":{"input":19998,"output":83,"cacheRead":5120,"cacheWrite":0,"reasoning":66,"totalTokens":25201,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1787736801084,"responseId":"be4fbb07d6f6430ba5f18c6381838cd3","rawStopReason":"tool_calls"}}`
	kimchiAutoRoutedReply = `{"type":"message","id":"5254f248","parentId":"e72a09d8","timestamp":"2026-10-27T10:41:47.524Z","message":{"role":"assistant","api":"openai-completions","provider":"kimchi-dev","model":"auto","usage":{"input":42986,"output":178,"cacheRead":0,"cacheWrite":0,"reasoning":154,"totalTokens":43164,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1793097699339,"responseId":"ad426a59f99d423e8897ccdf8895d18a","responseModel":"deepseek-v4-flash-0731","rawStopReason":"stop","durationMs":8180,"thinkingLevel":"medium"}}`
	kimchiCodexReply      = `{"type":"message","id":"ba087fe9","parentId":"e812698f","timestamp":"2026-08-09T04:46:07.898Z","message":{"role":"assistant","api":"openai-codex-responses","provider":"openai-codex","model":"gpt-5.3-codex-spark","usage":{"input":19219,"output":82,"cacheRead":0,"cacheWrite":0,"totalTokens":19301,"cost":{"input":0.033633249999999996,"output":0.001148,"cacheRead":0,"cacheWrite":0,"total":0.03478125}},"stopReason":"stop","timestamp":1786250762167,"responseId":"resp_0b9df18b65f8b7da016a78060c8c788191b0f14dad864fb5d0"}}`
	kimchiAbortedReply    = `{"type":"message","id":"a80fef52","parentId":"15ce2ec0","timestamp":"2026-08-13T19:49:52.916Z","message":{"role":"assistant","api":"openai-completions","provider":"kimchi-dev","model":"kimi-k2.7","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"aborted","timestamp":1786650590656}}`
)

func TestParseOMPSessionRecordsRepliesAndAuxiliaryCalls(t *testing.T) {
	source := usageSource(domain.UsageSourceOMPSession)
	source.NativeRootID = "01a048bb-551d-7000-8b7e-1a07961198e0"
	records := []jsonlRecord{
		{Offset: 0, Data: []byte(ompTitleSlotRecord)},
		{Offset: 256, Data: []byte(ompSessionRecord)},
		{Offset: 500, Data: []byte(ompZaiCachedReply)},
		{Offset: 900, Data: []byte(ompFailedReply)},
		{Offset: 1300, Data: []byte(ompFailedJudgeCall)},
		{Offset: 1700, Data: []byte(ompJudgeCall)},
	}
	result := parseRecords(source, records, 2100, time.Now())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}

	reply := result.Events[0]
	if reply.ProviderID != domain.UsageProviderOpenAI || reply.BillingProviderID != "zai" ||
		reply.BillingProviderSource != domain.UsageBillingProviderObserved || reply.ModelID != "glm-5.2" ||
		reply.MeasurementKind != domain.UsageMeasurementNativeReported ||
		reply.CreatedAt != time.Date(2026, 8, 28, 14, 25, 56, 303_000_000, time.UTC) {
		t.Fatalf("reply event = %+v", reply)
	}
	assertPiTokens(t, reply.Tokens, 41368, 40896, 472, 426)
	if costs := reply.Costs; costs.PricingVersion != ompPricingVersion ||
		costs.EstimatedCostNanos == nil || *costs.EstimatedCostNanos != 13_168_160 ||
		*costs.InputCostNanos != 660_800 || *costs.CachedInputCostNanos != 10_632_960 ||
		*costs.OutputCostNanos != 1_874_400 {
		t.Fatalf("reply costs = %+v", costs)
	}

	judge := result.Events[1]
	if judge.ProviderID != domain.UsageProviderOpenAI || judge.BillingProviderID != "zai" ||
		judge.ModelID != "glm-5.2" || judge.SourceEventKey == reply.SourceEventKey {
		t.Fatalf("judge event = %+v", judge)
	}
	assertPiTokens(t, judge.Tokens, 812, 0, 812, 9)
	if costs := judge.Costs; costs.PricingVersion != ompPricingVersion ||
		costs.EstimatedCostNanos == nil || *costs.EstimatedCostNanos != 1_176_400 {
		t.Fatalf("judge costs = %+v", costs)
	}
}

func TestParsePiIgnoresOMPAuxiliaryRecords(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	source.NativeRootID = "pi-session"
	result := parseRecords(source, []jsonlRecord{{Data: []byte(ompJudgeCall)}}, 100, time.Now())
	if result.err != nil || len(result.Events) != 0 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseOMPAnthropicReplyUsesAnthropicCounters(t *testing.T) {
	source := usageSource(domain.UsageSourceOMPSession)
	source.NativeRootID = "omp-session"
	record := jsonlRecord{Data: []byte(`{"type":"message","id":"e025073d","timestamp":"2026-08-29T17:33:31.277Z","message":{"role":"assistant","api":"anthropic-messages","provider":"anthropic","model":"claude-opus-4-8","usage":{"input":11,"output":7,"cacheRead":5,"cacheWrite":3,"totalTokens":26,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop"}}`)}
	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil || len(result.Events) != 1 {
		t.Fatalf("result = %+v", result)
	}
	event := result.Events[0]
	if event.ProviderID != domain.UsageProviderAnthropic || event.BillingProviderID != "anthropic" ||
		event.ModelID != "claude-opus-4-8" {
		t.Fatalf("event = %+v", event)
	}
	assertPiTokens(t, event.Tokens, 19, 5, 14, 7)
	if event.Costs.EstimatedCostNanos != nil || event.Costs.PricingVersion != "" {
		t.Fatalf("a zero OMP cost must be left for the catalog: %+v", event.Costs)
	}
}

func TestParseKimchiSessionRecordsEveryProvider(t *testing.T) {
	source := usageSource(domain.UsageSourceKimchiSession)
	source.NativeRootID = "019ffc6e-fcb3-7f68-a162-85693427d059"
	records := []jsonlRecord{
		{Offset: 0, Data: []byte(kimchiSessionRecord)},
		{Offset: 200, Data: []byte(kimchiGatewayReply)},
		{Offset: 600, Data: []byte(kimchiAutoRoutedReply)},
		{Offset: 1000, Data: []byte(kimchiCodexReply)},
		{Offset: 1400, Data: []byte(kimchiAbortedReply)},
	}
	result := parseRecords(source, records, 1800, time.Now())
	if result.err != nil || len(result.Events) != 3 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}

	gateway := result.Events[0]
	if gateway.ProviderID != domain.UsageProviderOpenAI || gateway.BillingProviderID != "kimchi-dev" ||
		gateway.BillingProviderSource != domain.UsageBillingProviderObserved || gateway.ModelID != "kimi-k2.7" ||
		gateway.MeasurementKind != domain.UsageMeasurementNativeReported {
		t.Fatalf("gateway event = %+v", gateway)
	}
	assertPiTokens(t, gateway.Tokens, 25118, 5120, 19998, 83)
	if gateway.Costs.EstimatedCostNanos == nil || *gateway.Costs.EstimatedCostNanos != 0 ||
		gateway.Costs.PricingVersion != kimchiPricingVersion {
		t.Fatalf("gateway zero cost must be kept as kimchi:reported $0: %+v", gateway.Costs)
	}
	if providerUsageTokens(t, gateway.ProviderUsageJSON, "reasoning") != 66 {
		t.Fatalf("gateway provider usage = %s", gateway.ProviderUsageJSON)
	}

	routed := result.Events[1]
	if routed.ModelID != "deepseek-v4-flash-0731" || routed.BillingProviderID != "kimchi-dev" {
		t.Fatalf("routed event = %+v", routed)
	}
	assertPiTokens(t, routed.Tokens, 42986, 0, 42986, 178)

	codex := result.Events[2]
	if codex.ProviderID != domain.UsageProviderOpenAI || codex.BillingProviderID != "openai-codex" ||
		codex.ModelID != "gpt-5.3-codex-spark" {
		t.Fatalf("codex event = %+v", codex)
	}
	assertPiTokens(t, codex.Tokens, 19219, 0, 19219, 82)
	if costs := codex.Costs; costs.PricingVersion != kimchiPricingVersion ||
		costs.EstimatedCostNanos == nil || *costs.EstimatedCostNanos != 34_781_250 ||
		*costs.InputCostNanos != 33_633_250 || *costs.CachedInputCostNanos != 0 ||
		*costs.OutputCostNanos != 1_148_000 {
		t.Fatalf("codex costs = %+v", costs)
	}
}

func TestParsePiFamilySharesNormalizationButNotIdentity(t *testing.T) {
	record := jsonlRecord{Data: []byte(ompZaiCachedReply)}
	kinds := map[domain.UsageSourceKind]string{
		domain.UsageSourcePiSession:         piPricingVersion,
		domain.UsageSourceOMPSession:        ompPricingVersion,
		domain.UsageSourceKimchiSession:     kimchiPricingVersion,
		domain.UsageSourcePrimeAgentSession: primeAgentPricingVersion,
	}
	keys := make(map[string]domain.UsageSourceKind)
	for kind, version := range kinds {
		source := usageSource(kind)
		source.NativeRootID = "shared-native-id"
		result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
		if result.err != nil || len(result.Events) != 1 || result.Cursor.AnomalyCount != 0 {
			t.Fatalf("%s result = %+v", kind, result)
		}
		event := result.Events[0]
		assertPiTokens(t, event.Tokens, 41368, 40896, 472, 426)
		if event.Costs.PricingVersion != version || *event.Costs.EstimatedCostNanos != 13_168_160 {
			t.Fatalf("%s costs = %+v", kind, event.Costs)
		}
		if other, duplicate := keys[event.SourceEventKey]; duplicate {
			t.Fatalf("%s and %s share event key %q", kind, other, event.SourceEventKey)
		}
		keys[event.SourceEventKey] = kind
	}
}

func TestParseOMPRejectsMalformedAuxiliaryCall(t *testing.T) {
	source := usageSource(domain.UsageSourceOMPSession)
	record := jsonlRecord{Data: []byte(`{"type":"model_usage","id":"no-model","provider":"zai","usage":{"input":5,"output":1,"cacheRead":0,"cacheWrite":0}}`)}
	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.Events) != 0 || result.Cursor.AnomalyCount != 1 ||
		result.Cursor.LastErrorCode != domain.UsageErrorMalformedJSONL {
		t.Fatalf("result = %+v", result)
	}
}

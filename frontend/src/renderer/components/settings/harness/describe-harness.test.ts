import type { TFunction } from "i18next";
import { describe, expect, it } from "vitest";
import type { AgentId } from "../../../lib/agent-options";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import { describeHarness } from "./describe-harness";

describe("configured harness authentication summary", () => {
	it.each([
		["mimo-code", undefined, undefined], // MiMo Auto needs no credentials.
		["claude-code", undefined, undefined],
		["codewhale", undefined, undefined],
		["memcode", undefined, undefined],
		["unreal-agent", undefined, undefined],
		["mimo-code", "credential_detection", "probe_timeout"],
		["hermes", "provider_validation", "provider_accepted"],
	])("uses a neutral label for %s with %s/%s", (agentId, method, code) => {
		expect(summary(agentId as AgentId, method, code)).toBe("settings.harness.installed");
	});

	it("keeps a renewable Claude token connected", () => {
		expect(summary("claude-code", "provider_validation", "token_renewal_pending")).toBe("settings.harness.loggedIn");
	});

	it("identifies unresolved native model selections as unverified", () => {
		expect(summary("mimo-code", "model_selection", "model_selection_unresolved")).toBe("settings.harness.health.authUnverified");
	});

	it.each(["mimo-code", "hermes"] as const)("identifies inconclusive provider checks for %s", (agentId) => {
		for (const code of ["provider_unsupported", "provider_inconclusive", "provider_unreachable", "probe_timeout", "probe_cancelled"]) {
			expect(summary(agentId, "provider_validation", code)).toBe("settings.harness.health.authUnverified");
		}
	});
});

function summary(agentId: AgentId, method?: string, code?: string) {
	const readinessAgent = agentReadiness(agentId, agentId, { authentication: "configured" });
	if (method && code) readinessAgent.authentication.diagnostics = [{
		method, code, reason: "Test observation", checkedAt: "2026-10-10T10:00:00Z", cached: false, durationMs: 0,
	}];
	return describeHarness({
		agentId, readinessAgent, t: ((key: string) => key) as TFunction, language: "en",
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null,
	}).authSummary;
}

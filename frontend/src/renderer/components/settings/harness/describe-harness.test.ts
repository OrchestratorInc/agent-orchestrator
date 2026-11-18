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

	// Mastra Code reports credentials it could not confirm with the selected
	// provider, including a provider limit, without claiming a signed-in state.
	it("keeps unconfirmed Mastra Code provider checks unverified", () => {
		for (const code of ["provider_unreachable", "provider_unsupported", "provider_rate_limited", "provider_quota_exhausted"]) {
			expect(summary("mastra-code", "provider_validation", code)).toBe("settings.harness.health.authUnverified");
		}
		expect(summary("mastra-code", "model_selection", "model_selection_unresolved")).toBe("settings.harness.health.authUnverified");
	});
});

// The Account and Health tabs both read this tone, so they never disagree.
describe("harness sign-in tone", () => {
	it("is green only for a fresh, successful sign-in check", () => {
		expect(tone({ authentication: "authorized" })).toBe("ok");
		expect(tone({ authentication: "authorized", freshness: "stale" })).toBe("off");
		expect(tone({ authentication: "authorized", freshness: "checking" })).toBe("off");
		expect(tone({ authentication: "authorized" }, true)).toBe("off");
		expect(tone({ authentication: "unauthorized" })).toBe("error");
		expect(tone({ authentication: "configured" })).toBe("off");
	});
});

function tone(options: Parameters<typeof agentReadiness>[2], authCheckFailed = false) {
	return describeHarness({
		agentId: "codex", readinessAgent: agentReadiness("codex", "codex", options), t: ((key: string) => key) as TFunction, language: "en",
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null, authCheckFailed,
	}).authTone;
}

describe("authorized harness whose login the CLI could not confirm", () => {
	it("says the login is not confirmed instead of Connected", () => {
		const details = describe_("cursor", "cursor-agent status", "account_unconfirmed", "authorized");
		expect(details.authSummary).toBe("settings.harness.loginUnconfirmed");
		expect(details.loginUnconfirmed).toBe(true);
		expect(details.authTone).toBe("off");
	});

	it("keeps a confirmed login Connected", () => {
		const details = describe_("cursor", "cursor-agent status", "cli_signed_in", "authorized");
		expect(details.authSummary).toBe("settings.harness.loggedIn");
		expect(details.loginUnconfirmed).toBe(false);
		expect(details.authTone).toBe("ok");
	});

	it("ignores the caveat when the harness is not signed in", () => {
		expect(describe_("cursor", "cursor-agent status", "account_unconfirmed", "configured").loginUnconfirmed).toBe(false);
	});
});

function summary(agentId: AgentId, method?: string, code?: string) {
	return describe_(agentId, method, code).authSummary;
}

function describe_(agentId: AgentId, method?: string, code?: string, authentication: "configured" | "authorized" = "configured") {
	const readinessAgent = agentReadiness(agentId, agentId, { authentication });
	if (method && code) readinessAgent.authentication.diagnostics = [{
		method, code, reason: "Test observation", checkedAt: "2026-10-10T10:00:00Z", cached: false, durationMs: 0,
	}];
	return describeHarness({
		agentId, readinessAgent, t: ((key: string) => key) as TFunction, language: "en",
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null,
	});
}

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type { CloudCpSession } from "../lib/cloud-cp";
import { cloudOrchestratorModel, liveCloudOrchestrator, useCloudOrchestratorModel } from "./useCloudOrchestratorModel";
import { cloudSessionsQueryKey } from "./useWorkspaceQuery";

const BASE_URL = "https://cloud.example.test";

function session(overrides: Partial<CloudCpSession>): CloudCpSession {
	return {
		id: "orch-1",
		orgId: "org-1",
		projectId: "project-1",
		kind: "orchestrator",
		harness: "claude-code",
		model: "opus",
		status: "working",
		isTerminated: false,
		createdAt: "2026-11-01T00:00:00Z",
		...overrides,
	} as CloudCpSession;
}

describe("liveCloudOrchestrator", () => {
	it("picks the project's newest running orchestrator", () => {
		const sessions = [
			session({ id: "old", createdAt: "2026-10-01T00:00:00Z" }),
			session({ id: "new", createdAt: "2026-11-02T00:00:00Z" }),
			session({ id: "stopped", createdAt: "2026-11-03T00:00:00Z", isTerminated: true, status: "terminated" }),
			session({ id: "worker", kind: "worker", createdAt: "2026-11-04T00:00:00Z" }),
			session({ id: "elsewhere", projectId: "project-2", createdAt: "2026-11-05T00:00:00Z" }),
		];
		expect(liveCloudOrchestrator(sessions, "project-1")?.id).toBe("new");
		expect(liveCloudOrchestrator(sessions, "project-3")).toBeUndefined();
		expect(liveCloudOrchestrator(undefined, "project-1")).toBeUndefined();
	});
});

describe("cloudOrchestratorModel", () => {
	it("prefers the Chat's model and effort, else the model the session started with", () => {
		const orchestrator = session({});
		expect(cloudOrchestratorModel(orchestrator, { model: "haiku", reasoningEffort: "high" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "haiku", reasoningEffort: "high" });
		expect(cloudOrchestratorModel(orchestrator, undefined)).toEqual({ sessionId: "orch-1", harness: "claude-code", model: "opus" });
		expect(cloudOrchestratorModel(session({ model: "" }), {})).toBeUndefined();
	});
});

describe("useCloudOrchestratorModel", () => {
	const settingsKey = "cloud-chat-settings:org-1:orch-1:claude-code";
	afterEach(() => window.localStorage.removeItem(settingsKey));

	function renderWith(sessions: CloudCpSession[] | undefined, enabled = true) {
		const queryClient = new QueryClient();
		if (sessions) queryClient.setQueryData([...cloudSessionsQueryKey, BASE_URL, "org-1"], sessions);
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		return renderHook(() => useCloudOrchestratorModel({ baseUrl: BASE_URL, orgId: "org-1", projectId: "project-1", enabled }), { wrapper });
	}

	it("reads the orchestrator from the cached session list and its Chat's saved settings", () => {
		window.localStorage.setItem(settingsKey, JSON.stringify({ model: "sonnet", reasoningEffort: "low", approvalMode: "auto" }));
		const { result } = renderWith([session({})]);
		expect(result.current).toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet", reasoningEffort: "low" });
	});

	it("names none until the session list has loaded, and none outside Cloud", () => {
		expect(renderWith(undefined).result.current).toBeUndefined();
		expect(renderWith([session({})], false).result.current).toBeUndefined();
	});
});

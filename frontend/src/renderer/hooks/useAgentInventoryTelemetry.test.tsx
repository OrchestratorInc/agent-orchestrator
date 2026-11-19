import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";

const capture = vi.hoisted(() => vi.fn(async (_event: string, _props?: unknown) => true));
const state = vi.hoisted(() => ({
	requirements: [{ id: "git", satisfied: true }, { id: "tmux", satisfied: true }, { id: "gh", satisfied: false }] as { id: string; satisfied: boolean }[],
	agents: [] as unknown[],
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: capture, telemetryStorage: () => window.localStorage }));
vi.mock("./useSystemRequirementsGate", () => ({
	githubAuthRequirementQueryOptions: { queryKey: ["github-auth-requirement"], queryFn: vi.fn() },
	useSystemRequirementsGate: () => ({ query: { data: { requirements: state.requirements } } }),
}));
vi.mock("./useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: state.agents } }) }));

import { useSetupCheckedTelemetry } from "./useAgentInventoryTelemetry";

function wrapper(client: QueryClient) {
	return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useSetupCheckedTelemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		capture.mockResolvedValue(true);
		window.localStorage.clear();
		state.agents = [agentReadiness("codex", "Codex", { installation: "installed", authentication: "authorized" })];
		state.requirements = [{ id: "git", satisfied: true }, { id: "tmux", satisfied: true }, { id: "gh", satisfied: false }];
	});

	it("sends once at first open, not again for the same result, and again on change", async () => {
		const client = new QueryClient();
		const first = renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(client) });
		await waitFor(() => expect(capture).toHaveBeenCalledTimes(1));
		expect(capture).toHaveBeenLastCalledWith("ao.setup.checked", expect.objectContaining({ git: true, gh: false, agents_installed: 1, agents_signed_in_ids: "codex" }));
		first.unmount();

		// Next launch, same machine state: nothing.
		const relaunch = renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(new QueryClient()) });
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(capture).toHaveBeenCalledTimes(1);
		relaunch.unmount();

		// gh installed and the GitHub probe has answered: one more.
		state.requirements = [{ id: "git", satisfied: true }, { id: "tmux", satisfied: true }, { id: "gh", satisfied: true }];
		const changedClient = new QueryClient();
		changedClient.setQueryData(["github-auth-requirement"], { id: "github-auth", satisfied: true });
		renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(changedClient) });
		await waitFor(() => expect(capture).toHaveBeenCalledTimes(2));
		expect(capture).toHaveBeenLastCalledWith("ao.setup.checked", expect.objectContaining({ gh: true, gh_signed_in: true }));
	});

	it("does not resend on a relaunch where the GitHub probe has not answered yet", async () => {
		const answered = new QueryClient();
		answered.setQueryData(["github-auth-requirement"], { id: "github-auth", satisfied: true });
		const first = renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(answered) });
		await waitFor(() => expect(capture).toHaveBeenCalledTimes(1));
		first.unmount();
		// Relaunch: same machine, GitHub probe not answered yet, then answered.
		const relaunch = new QueryClient();
		const second = renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(relaunch) });
		await new Promise((resolve) => setTimeout(resolve, 0));
		relaunch.setQueryData(["github-auth-requirement"], { id: "github-auth", satisfied: true });
		second.rerender();
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(capture).toHaveBeenCalledTimes(1);
	});

	it("waits for agent probes to settle before the first send", async () => {
		state.agents = [agentReadiness("codex", "Codex", { installation: "installed", authentication: "authorized", freshness: "checking" })];
		const client = new QueryClient();
		const view = renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(client) });
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(capture).not.toHaveBeenCalled();
		state.agents = [agentReadiness("codex", "Codex", { installation: "installed", authentication: "authorized" })];
		view.rerender();
		await waitFor(() => expect(capture).toHaveBeenCalledTimes(1));
	});

	it("retries on a later launch when the capture was not sent", async () => {
		capture.mockResolvedValueOnce(false);
		const once = renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(new QueryClient()) });
		await waitFor(() => expect(capture).toHaveBeenCalledTimes(1));
		once.unmount();
		renderHook(() => useSetupCheckedTelemetry(), { wrapper: wrapper(new QueryClient()) });
		await waitFor(() => expect(capture).toHaveBeenCalledTimes(2));
	});
});

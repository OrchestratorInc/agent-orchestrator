import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { TriageChoice, TriageRun } from "../../../shared/triage";
import { queryClient } from "../../lib/query-client";
import { useTriageStore } from "../../stores/triage-store";
import { TriageRuntime } from "./TriageRuntime";

const mock = vi.hoisted(() => ({ catalog: vi.fn(), configure: vi.fn(), list: vi.fn(), subscribe: vi.fn(), start: vi.fn() }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { triage: mock } }));
vi.mock("./TriageConversation", () => ({ useTriageStateLabel: () => "Working" }));

const native: TriageChoice = { harness: "codex", label: "Codex", models: [{ id: "gpt-5.4", label: "GPT 5.4", default: true }],
	accounts: [{ id: "work", label: "Work", default: true }], available: true, sessionMode: "chat" };
const ready = (id: string) => ({ id, label: id, authentication: { state: "configured", checkedAt: "2026-10-19T10:00:00Z" }, installation: { state: "installed" } });
function configureCache() {
	queryClient.setQueryData(["settings"], {});
	queryClient.setQueryData(["agent-readiness"], { agents: [ready("codex"), ready("command-code")] });
	queryClient.setQueryData(["agent-models", "command-code", ""], { models: [{ id: "configured-model", label: "Configured model", isDefault: true }], fetchedAt: "2026-10-19T10:00:00Z" });
}

beforeEach(() => {
	vi.clearAllMocks();
	queryClient.clear();
	localStorage.clear();
	useTriageStore.setState(useTriageStore.getInitialState(), true);
	mock.catalog.mockResolvedValue([native, { ...native, harness: "command-code", models: [] }]);
	mock.configure.mockResolvedValue(undefined);
	mock.list.mockResolvedValue([]);
	mock.subscribe.mockReturnValue(() => {});
});

it("retains configured agents for daemon outages before the launcher has ever opened", async () => {
	configureCache();
	useTriageStore.setState({ selection: { harness: "command-code", model: "configured-model" } });
	render(<TriageRuntime />);
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(1));
	expect(mock.configure.mock.calls[0][0]).toEqual(expect.arrayContaining([
		expect.objectContaining({ harness: "codex", models: native.models, accounts: native.accounts }),
		expect.objectContaining({ harness: "command-code", models: [expect.objectContaining({ id: "configured-model" })] }),
	]));
	expect(useTriageStore.getState()).toMatchObject({ selection: { harness: "command-code", model: "configured-model" }, catalogLoaded: false, loading: false, error: null });
	expect(mock.start).not.toHaveBeenCalled();
});

it.each([undefined, "ordinary-session"])("never advertises a separate support conversation for a normal session launch %s", async (sessionId) => {
	const handoff: TriageRun = { id: "handoff", version: 1, execution: "session", sessionId, state: "needs_input", activity: "Opening normal agent",
		createdAt: "2026-10-19T10:00:00Z", updatedAt: "2026-10-19T10:00:00Z", request: { harness: "codex", publishAuthorized: true }, messages: [] };
	mock.list.mockResolvedValue([handoff]);
	const view = render(<TriageRuntime />);
	await waitFor(() => expect(useTriageStore.getState().runs).toHaveLength(1));
	act(() => useTriageStore.getState().receive(handoff));
	expect(view.container.querySelector("a")).toBeNull();
});

it("waits for existing local display data and never fetches missing catalogs", async () => {
	render(<TriageRuntime />);
	expect(mock.catalog).not.toHaveBeenCalled();
	act(() => {
		queryClient.setQueryData(["settings", "remote-host"], {});
		queryClient.setQueryData(["agent-readiness", "remote-host"], { agents: [ready("command-code")] });
	});
	expect(mock.catalog).not.toHaveBeenCalled();
	act(() => configureCache());
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(1));
	expect(queryClient.getQueryState(["agent-models", "codex", ""])).toBeUndefined();
	expect(mock.start).not.toHaveBeenCalled();
});

it("serializes changes so the latest enabled-model configuration is the one retained", async () => {
	configureCache();
	let release!: () => void;
	mock.configure.mockImplementationOnce(() => new Promise<void>((resolve) => { release = resolve; }));
	render(<TriageRuntime />);
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(1));
	act(() => queryClient.setQueryData(["settings"], { harnessDefaults: { "command-code": { disabledModels: ["configured-model"] } } }));
	expect(mock.configure).toHaveBeenCalledTimes(1);
	await act(async () => { release(); });
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(2));
	expect(mock.configure.mock.calls[1][0]).toEqual(expect.arrayContaining([
		expect.objectContaining({ harness: "command-code", models: [] }),
	]));
	expect(mock.start).not.toHaveBeenCalled();
});

it("does not rewrite unchanged configuration or persist updates after unmount", async () => {
	configureCache();
	const view = render(<TriageRuntime />);
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(1));
	await act(async () => {
		queryClient.setQueryData(["agent-readiness"], { agents: [ready("codex"), ready("command-code")] });
		queryClient.setQueryData(["workspaces"], []);
		queryClient.setQueryData(["agent-models", "command-code", "remote-project"], { models: [] });
	});
	expect(mock.configure).toHaveBeenCalledTimes(1);
	view.unmount();
	act(() => queryClient.setQueryData(["settings"], { harnessDefaults: { codex: { disabledModels: ["gpt-5.4"] } } }));
	expect(mock.configure).toHaveBeenCalledTimes(1);
});

it("retains an existing account change without changing the launcher selection", async () => {
	configureCache();
	useTriageStore.setState({ selection: { harness: "codex", model: "gpt-5.4", accountId: "work" } });
	render(<TriageRuntime />);
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(1));
	act(() => queryClient.setQueryData(["agent-accounts"], [{ id: "new-work", provider: "codex", signedIn: true, default: true, device: false, email: "work@example.com" }]));
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(2));
	expect(mock.configure.mock.calls[1][0]).toEqual(expect.arrayContaining([
		expect.objectContaining({ harness: "codex", accounts: [expect.objectContaining({ id: "new-work", default: true })] }),
	]));
	expect(useTriageStore.getState().selection?.accountId).toBe("work");
	expect(mock.start).not.toHaveBeenCalled();
});

it("persists an authoritative empty catalog after the same model list was only missing", async () => {
	mock.catalog.mockResolvedValue([{ ...native, models: [], modelsMissing: true }]);
	queryClient.setQueryData(["settings"], {});
	queryClient.setQueryData(["agent-readiness"], { agents: [ready("codex")] });
	render(<TriageRuntime />);
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(1));
	expect(mock.configure.mock.calls[0][0][0]).toMatchObject({ harness: "codex", models: [], modelsMissing: true });
	act(() => queryClient.setQueryData(["agent-models", "codex", ""], { models: [], fetchedAt: "2026-10-19T10:00:00Z" }));
	await waitFor(() => expect(mock.configure).toHaveBeenCalledTimes(2));
	expect(mock.configure.mock.calls[1][0][0].models).toEqual([]);
	expect(mock.configure.mock.calls[1][0][0].modelsMissing).not.toBe(true);
	expect(mock.start).not.toHaveBeenCalled();
});

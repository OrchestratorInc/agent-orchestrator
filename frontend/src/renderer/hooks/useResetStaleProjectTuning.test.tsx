import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, PUT: putMock } }));
vi.mock("../lib/host-clients", () => ({ clientForHost: () => ({ GET: getMock, PUT: putMock }) }));

import { useUiStore } from "../stores/ui-store";
import { useResetStaleProjectTuning } from "./useResetStaleProjectTuning";

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
}

const catalog = { refreshState: "idle", models: [{ id: "claude-sonnet-5-5", efforts: ["low", "high"] }] };
const config = { env: { KEEP: "1" }, worker: { agent: "claude-code", agentConfig: { model: "claude-sonnet-5-5", effort: "max" } } };

async function confirmReset() {
	await waitFor(() => expect(useUiStore.getState().globalToasts[0]?.action?.label).toBe("Reset saved settings"));
	expect(getMock).not.toHaveBeenCalled();
	expect(putMock).not.toHaveBeenCalled();
	const toast = useUiStore.getState().globalToasts[0];
	await act(async () => {
		toast.action?.run();
		useUiStore.getState().dismissGlobalToast(toast.nonce);
	});
}

describe("useResetStaleProjectTuning", () => {
	beforeEach(() => {
		getMock.mockReset().mockResolvedValue({ data: { status: "ok", project: { id: "mer", name: "Mer", config } } });
		putMock.mockReset().mockResolvedValue({ data: {} });
		useUiStore.setState({ globalToasts: [] });
	});

	it("resets unsupported worker effort once, keeping the model and remaining config", async () => {
		const props = { projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code", config, catalog };
		const { rerender } = renderHook((p) => useResetStaleProjectTuning(p), { wrapper, initialProps: props });
		await confirmReset();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "mer" } },
			body: { displayName: "Mer", config: { env: { KEEP: "1" }, worker: { agent: "claude-code", agentConfig: { model: "claude-sonnet-5-5" } } } },
		});
		await waitFor(() => expect(useUiStore.getState().globalToasts).toHaveLength(1));
		expect(useUiStore.getState().globalToasts[0].title).toBe("Project model settings reset");
		rerender({ ...props });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(putMock).toHaveBeenCalledTimes(1);
	});

	it.each([
		{
			name: "removed project model with worker effort",
			saved: { agentConfig: { model: "claude-gone-4", effort: "max" }, worker: { agentConfig: { effort: "high" } } },
			expected: { agentConfig: { effort: "max" }, worker: { agentConfig: {} } },
		},
		{
			name: "unsupported project effort with worker model",
			saved: { agentConfig: { effort: "max" }, worker: { agentConfig: { model: "claude-sonnet-5-5" } } },
			expected: { agentConfig: {}, worker: { agentConfig: { model: "claude-sonnet-5-5" } } },
		},
		{
			name: "unsupported worker effort with project model",
			saved: { agentConfig: { model: "claude-sonnet-5-5" }, worker: { agentConfig: { effort: "max" } } },
			expected: { agentConfig: { model: "claude-sonnet-5-5" }, worker: { agentConfig: {} } },
		},
		{
			name: "removed worker model with project effort",
			saved: { agentConfig: { effort: "high" }, worker: { agentConfig: { model: "claude-gone-4" } } },
			expected: { agentConfig: {}, worker: { agentConfig: {} } },
		},
	])("resets each effective field in its owning slot: $name", async ({ saved, expected }) => {
		const savedConfig = { env: { KEEP: "1" }, ...saved };
		getMock.mockResolvedValue({ data: { status: "ok", project: { id: "mer", name: "Mer", config: savedConfig } } });
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: saved.agentConfig?.model === "claude-gone-4" || saved.worker?.agentConfig?.model === "claude-gone-4" ? "memcode" : "claude-code", agentLabel: "Agent", projectAgent: saved.agentConfig?.model === "claude-gone-4" || saved.worker?.agentConfig?.model === "claude-gone-4" ? "memcode" : "claude-code",
			config: savedConfig, catalog: saved.agentConfig?.model === "claude-gone-4" || saved.worker?.agentConfig?.model === "claude-gone-4" ? { ...catalog, source: "official", customModelEntry: "none" } : catalog,
		}), { wrapper });
		await confirmReset();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "mer" } },
			body: { displayName: "Mer", config: { env: { KEEP: "1" }, ...expected } },
		});
	});

	it("keeps overridden project settings when the effective worker pair is valid", async () => {
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code",
			config: { agentConfig: { model: "claude-gone-4", effort: "max" }, worker: { agentConfig: { model: "claude-sonnet-5-5", effort: "high" } } },
			catalog,
		}), { wrapper });
		expect(getMock).not.toHaveBeenCalled();
		expect(putMock).not.toHaveBeenCalled();
	});

	it.each([
		{ agentConfig: { model: "claude-gone-4", effort: "high" }, worker: { agentConfig: { model: "claude-gone-4" } } },
		{ agentConfig: { model: "claude-gone-4", effort: "high" }, worker: { agentConfig: { effort: "high" } } },
		{ agentConfig: { model: "claude-sonnet-5-5", effort: "high" } },
		{ agentConfig: { model: "claude-gone-4", effort: "max" } },
	])("does not write when an effective value or source changed before the re-read (%#)", async (currentConfig) => {
		getMock.mockResolvedValue({ data: { status: "ok", project: { id: "mer", name: "Mer", config: currentConfig } } });
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code",
			config: { agentConfig: { model: "claude-sonnet-5-5", effort: "max" } }, catalog,
		}), { wrapper });
		await confirmReset();
		await waitFor(() => expect(getMock).toHaveBeenCalledOnce());
		expect(putMock).not.toHaveBeenCalled();
	});

	it("clears saved effort for a no-effort Claude model while keeping the model", async () => {
		const saved = { worker: { agentConfig: { model: "claude-haiku-4-5", effort: "high" } } };
		getMock.mockResolvedValue({ data: { status: "ok", project: { id: "mer", name: "Mer", config: saved } } });
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code",
			config: saved, catalog: { models: [{ id: "claude-haiku-4-5" }] },
		}), { wrapper });
		await confirmReset();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "mer" } },
			body: { displayName: "Mer", config: { worker: { agentConfig: { model: "claude-haiku-4-5" } } } },
		});
	});

	it("keeps settings when the user dismisses the reset proposal", async () => {
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code", config, catalog,
		}), { wrapper });
		await waitFor(() => expect(useUiStore.getState().globalToasts).toHaveLength(1));
		const toast = useUiStore.getState().globalToasts[0];
		expect(toast.dismissible).toBe(true);
		expect(toast.durationMs).toBe(0);
		act(() => useUiStore.getState().dismissGlobalToast(toast.nonce));
		expect(getMock).not.toHaveBeenCalled();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("reports a failed confirmed reset without claiming settings changed", async () => {
		putMock.mockResolvedValue({ error: { code: "SAVE_FAILED" } });
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code", config, catalog,
		}), { wrapper });
		await confirmReset();
		await waitFor(() => expect(useUiStore.getState().globalToasts[0]?.title).toBe("Could not reset saved model settings"));
		expect(useUiStore.getState().globalToasts[0].tone).toBe("error");
		const retry = useUiStore.getState().globalToasts[0];
		expect(retry.action?.label).toBe("Retry");
		putMock.mockResolvedValue({ data: {} });
		await act(async () => { retry.action?.run(); });
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(2));
	});

	it("preserves saved model and effort when Claude Code only needs updating", () => {
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code",
			config: { agentConfig: { model: "claude-sonnet-5-5", effort: "max" } },
			catalog: { models: [{ id: "claude-sonnet-5-5", efforts: ["high"], requiresVersion: "99.0.0" }] },
		}), { wrapper });
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
		expect(getMock).not.toHaveBeenCalled();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("does not apply an old proposal after catalog capabilities change", async () => {
		const props = { projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code", config, catalog };
		const { rerender } = renderHook((p) => useResetStaleProjectTuning(p), { wrapper, initialProps: props });
		const toast = useUiStore.getState().globalToasts[0];
		rerender({ ...props, catalog: { ...catalog, models: [{ id: "claude-sonnet-5-5", efforts: ["high", "max"] }] } });
		await act(async () => { toast.action?.run(); });
		expect(getMock).not.toHaveBeenCalled();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("leaves valid settings and other agents alone", async () => {
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code",
			config: { worker: { agentConfig: { model: "claude-sonnet-5-5", effort: "high" } } }, catalog,
		}), { wrapper });
		renderHook(() => useResetStaleProjectTuning({
			projectId: "mer", agent: "codex", agentLabel: "Codex", projectAgent: "claude-code", config, catalog,
		}), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(getMock).not.toHaveBeenCalled();
		expect(putMock).not.toHaveBeenCalled();
	});
});

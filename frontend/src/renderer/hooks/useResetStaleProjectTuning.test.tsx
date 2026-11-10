import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
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
const config = { env: { KEEP: "1" }, worker: { agent: "claude-code", agentConfig: { model: "claude-gone-4", effort: "high" } } };

describe("useResetStaleProjectTuning", () => {
	beforeEach(() => {
		getMock.mockReset().mockResolvedValue({ data: { status: "ok", project: { id: "mer", name: "Mer", config } } });
		putMock.mockReset().mockResolvedValue({ data: {} });
		useUiStore.setState({ globalToasts: [] });
	});

	it("resets a saved worker model the catalog no longer lists, once, keeping the rest of the config", async () => {
		const props = { projectId: "mer", agent: "claude-code", agentLabel: "Claude Code", projectAgent: "claude-code", config, catalog };
		const { rerender } = renderHook((p) => useResetStaleProjectTuning(p), { wrapper, initialProps: props });
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "mer" } },
			body: { displayName: "Mer", config: { env: { KEEP: "1" }, worker: { agent: "claude-code", agentConfig: {} } } },
		});
		await waitFor(() => expect(useUiStore.getState().globalToasts).toHaveLength(1));
		expect(useUiStore.getState().globalToasts[0].body).toContain("claude-gone-4 is no longer available for Claude Code");
		rerender({ ...props });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(putMock).toHaveBeenCalledTimes(1);
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

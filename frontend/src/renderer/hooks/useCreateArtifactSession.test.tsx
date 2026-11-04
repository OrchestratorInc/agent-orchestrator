import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { rememberTaskComposerPreference } from "../lib/task-composer-preferences";
import { useUiStore } from "../stores/ui-store";
import { useCreateArtifactSession } from "./useCreateArtifactSession";

const cloud = vi.hoisted(() => ({ listUserProviderConnections: vi.fn(), createSession: vi.fn() }));
const navigate = vi.hoisted(() => vi.fn());
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: cloud, ready: true, baseUrl: "https://cp.test" }) }));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigate }));
vi.mock("./useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: [] } }) }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: () => Promise.resolve() }));
const settings = vi.hoisted(() => ({ current: { defaultSessionMode: "chat" } as { defaultSessionMode?: string } }));
vi.mock("./useSettings", () => ({ useSettings: () => ({ settings: settings.current }) }));

function connection(provider: string) {
	return { id: provider, provider, label: "default", config: {}, validationState: "valid", createdAt: "", updatedAt: "" };
}

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
}

describe("useCreateArtifactSession for a cloud project", () => {
	beforeEach(() => {
		cloud.listUserProviderConnections.mockReset().mockResolvedValue({ providerConnections: [connection("claude-code"), connection("codex")] });
		window.localStorage.clear();
		cloud.createSession.mockReset().mockResolvedValue({ session: { id: "s-1" } });
		navigate.mockReset();
	});

	it("starts an artifact session with the first connected agent", async () => {
		const { result } = renderHook(() => useCreateArtifactSession(), { wrapper });
		await act(() => result.current.create("p-1", "org-1"));
		expect(cloud.createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({
			projectId: "p-1", kind: "worker", harness: "claude-code", requestedOutput: "artifact", prompt: "",
			defaultInterfaceMode: "chat",
		}));
		expect(navigate).toHaveBeenCalledWith("p-1", "s-1");
	});

	it("uses codex when it is the only connected agent", async () => {
		cloud.listUserProviderConnections.mockResolvedValue({ providerConnections: [connection("codex")] });
		const { result } = renderHook(() => useCreateArtifactSession(), { wrapper });
		await act(() => result.current.create("p-1", "org-1"));
		expect(cloud.createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ harness: "codex" }));
	});

	it("prefers the remembered agent while it is connected", async () => {
		rememberTaskComposerPreference("p-1", "codex", { model: "", mode: "" });
		const { result } = renderHook(() => useCreateArtifactSession(), { wrapper });
		await act(() => result.current.create("p-1", "org-1"));
		expect(cloud.createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ harness: "codex" }));
	});

	it("shows an error and creates nothing when no agent is connected", async () => {
		cloud.listUserProviderConnections.mockResolvedValue({ providerConnections: [] });
		const { result } = renderHook(() => useCreateArtifactSession(), { wrapper });
		await act(() => result.current.create("p-1", "org-1"));
		expect(cloud.createSession).not.toHaveBeenCalled();
		expect(navigate).not.toHaveBeenCalled();
	});

	it("shows the control plane's refusal and opens nothing", async () => {
		cloud.createSession.mockRejectedValue(new Error("Your plan has no capacity for another session."));
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast");
		const { result } = renderHook(() => useCreateArtifactSession(), { wrapper });
		await act(() => result.current.create("p-1", "org-1"));
		expect(toast).toHaveBeenCalledWith("Your plan has no capacity for another session.", undefined, "error");
		expect(navigate).not.toHaveBeenCalled();
		expect(result.current.pending).toBe(false);
	});
});
